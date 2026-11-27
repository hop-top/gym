package eventbus_test

import (
	"testing"

	"github.com/spf13/viper"

	"hop.top/gym/internal/eventbus"
)

func TestIsStrict(t *testing.T) {
	cases := []struct {
		name string
		set  func(vp *viper.Viper)
		want bool
	}{
		{name: "default-false", set: func(*viper.Viper) {}, want: false},
		{name: "explicit-strict-key-true", set: func(vp *viper.Viper) {
			vp.Set(eventbus.StrictKey, true)
		}, want: true},
		{name: "explicit-strict-key-false", set: func(vp *viper.Viper) {
			vp.Set(eventbus.StrictKey, false)
		}, want: false},
		{name: "config-form-true", set: func(vp *viper.Viper) {
			vp.Set("gym.strict", true)
		}, want: true},
		{name: "flag-wins-over-config", set: func(vp *viper.Viper) {
			vp.Set("gym.strict", true)
			vp.Set(eventbus.StrictKey, false)
		}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vp := viper.New()
			tc.set(vp)
			if got := eventbus.IsStrict(vp); got != tc.want {
				t.Errorf("IsStrict = %v; want %v", got, tc.want)
			}
		})
	}
}
