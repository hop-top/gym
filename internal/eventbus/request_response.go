package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"hop.top/kit/go/runtime/bus"
)

// ErrTimeout is returned by RequestResponse when no correlated reply
// arrives before the deadline.
var ErrTimeout = errors.New("eventbus: request timed out")

// RequestResponse publishes a request on reqTopic and blocks until a
// matching response arrives on respPattern (a kit/bus subscribe
// pattern, e.g. "rsx.repo.policy.evaluated").
//
// Correlation is by request_id: the helper either uses the RequestID
// already set on the payload (if it carries a `request_id` JSON field)
// or assigns a fresh uuid v7. The same identifier is matched against
// the response payload's `request_id` field via re-marshal-hop.
//
// Semantics:
//   - Subscribers MUST be idempotent on request_id (kit/bus has no
//     dedupe).
//   - On timeout the helper returns (zero, ErrTimeout) — the caller
//     decides whether that's fail-open or fail-closed (see strict.go).
//
// payload may be a struct value with a `request_id` json field. If
// the field is empty, RequestResponse fills it with a fresh uuid v7
// before publishing; the caller never sees the mutated value.
func (b *Bus) RequestResponse(
	ctx context.Context,
	reqTopic bus.Topic,
	respPattern string,
	payload any,
	timeout time.Duration,
) (Response, error) {
	if b == nil || b.bus == nil {
		return Response{}, errors.New("eventbus: nil bus")
	}

	id, payloadWithID, err := ensureRequestID(payload)
	if err != nil {
		return Response{}, fmt.Errorf("eventbus: request_id: %w", err)
	}

	ch := make(chan bus.Event, 1)
	unsub := b.bus.Subscribe(respPattern, func(_ context.Context, e bus.Event) error {
		got, ok := extractRequestID(e.Payload)
		if !ok || got != id {
			return nil
		}
		select {
		case ch <- e:
		default:
		}
		return nil
	})
	defer unsub()

	if err := b.Publish(ctx, reqTopic, payloadWithID); err != nil {
		return Response{}, err
	}

	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case e := <-ch:
		return Response{Event: e, RequestID: id}, nil
	case <-deadline.Done():
		if errors.Is(deadline.Err(), context.DeadlineExceeded) {
			return Response{RequestID: id}, ErrTimeout
		}
		return Response{RequestID: id}, deadline.Err()
	}
}

// Response is the result of a successful RequestResponse round-trip.
// Decode unmarshals the payload into a typed struct using the
// re-marshal-hop pattern documented in kit/bus/doc.go.
type Response struct {
	Event     bus.Event
	RequestID string
}

// Decode re-marshal-hops the response payload into target. target must
// be a non-nil pointer to a struct.
func (r Response) Decode(target any) error {
	if target == nil {
		return errors.New("eventbus: nil decode target")
	}
	raw, err := json.Marshal(r.Event.Payload)
	if err != nil {
		return fmt.Errorf("eventbus: marshal payload: %w", err)
	}
	return json.Unmarshal(raw, target)
}

// ensureRequestID returns (id, payloadWithID, error). If payload has a
// `request_id` json field that is non-empty, it is reused; otherwise a
// fresh uuid v7 is assigned. Map and struct payloads are both
// supported; anything else gets a fresh id and is left untouched
// (callers decide how to attach it).
func ensureRequestID(payload any) (string, any, error) {
	if payload == nil {
		id := newRequestID()
		return id, map[string]any{"request_id": id}, nil
	}

	// Map-of-string: mutate in-place.
	if m, ok := payload.(map[string]any); ok {
		if existing, ok := m["request_id"].(string); ok && existing != "" {
			return existing, m, nil
		}
		id := newRequestID()
		m["request_id"] = id
		return id, m, nil
	}

	// Struct path: use reflect to set RequestID field if present and
	// empty. We round-trip to JSON to keep this generic and handle
	// both addressable structs and pointer-to-struct.
	id, set, err := setOrReadRequestIDByReflect(payload)
	if err == nil {
		return id, set, nil
	}

	// Fallback: marshal, patch JSON, return as map.
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		// Not an object payload: caller passed a scalar/array;
		// generate a fresh id and let the caller correlate manually.
		return newRequestID(), payload, nil
	}
	if existing, ok := m["request_id"].(string); ok && existing != "" {
		return existing, m, nil
	}
	id = newRequestID()
	m["request_id"] = id
	return id, m, nil
}

// setOrReadRequestIDByReflect mutates the RequestID field on the
// payload struct (if any). Returns the resulting id and the (possibly
// new) value to publish.
func setOrReadRequestIDByReflect(payload any) (string, any, error) {
	v := reflect.ValueOf(payload)
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return "", nil, errors.New("nil pointer payload")
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", nil, errors.New("not a struct")
	}
	// Find a field with json tag "request_id" or name RequestID.
	t := v.Type()
	idx := -1
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Tag.Get("json") != "" {
			tag := f.Tag.Get("json")
			// strip ,omitempty etc
			name := tag
			for j, r := range tag {
				if r == ',' {
					name = tag[:j]
					break
				}
			}
			if name == "request_id" {
				idx = i
				break
			}
		}
		if f.Name == "RequestID" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return "", nil, errors.New("no request_id field")
	}

	field := v.Field(idx)
	if field.Kind() != reflect.String {
		return "", nil, errors.New("request_id field is not a string")
	}
	if existing := field.String(); existing != "" {
		return existing, payload, nil
	}
	if !field.CanSet() {
		// Caller passed by-value; we have to rebuild.
		copy := reflect.New(t).Elem()
		copy.Set(v)
		copy.Field(idx).SetString(newRequestID())
		return copy.Field(idx).String(), copy.Interface(), nil
	}
	id := newRequestID()
	field.SetString(id)
	return id, payload, nil
}

// extractRequestID pulls the request_id off any payload shape kit/bus
// might surface (typed struct in-process, map[string]any cross-process).
func extractRequestID(payload any) (string, bool) {
	if payload == nil {
		return "", false
	}
	if m, ok := payload.(map[string]any); ok {
		s, ok := m["request_id"].(string)
		return s, ok && s != ""
	}
	v := reflect.ValueOf(payload)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		// Try generic JSON decode as a last resort.
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", false
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return "", false
		}
		s, ok := m["request_id"].(string)
		return s, ok && s != ""
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Name != "RequestID" && jsonName(f.Tag.Get("json")) != "request_id" {
			continue
		}
		if v.Field(i).Kind() != reflect.String {
			continue
		}
		s := v.Field(i).String()
		return s, s != ""
	}
	return "", false
}

func jsonName(tag string) string {
	for j, r := range tag {
		if r == ',' {
			return tag[:j]
		}
	}
	return tag
}

// newRequestID returns a uuid v7. Falls back to v4 if v7 is somehow
// unavailable on the platform.
func newRequestID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}
