BIN ?= gym

.PHONY: build test fmt vet tidy promote promote-beta promote-rc promote-open-line

build:
	go build -o $(BIN) ./cmd/gym

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy


# Target-versioning steps (RELEASING.md), recorded in the release-please
# config by hop-top/.github's promote.js and committed on the current branch
# (push it to next). DRY_RUN=1 prints only; NO_COMMIT=1 writes, no commit.
#   make promote-beta
#   make promote-rc DRY_RUN=1
#   make promote-open-line LINE=1.0.0
#   make promote ARGS='clear channel --package gym'
PROMOTE_SELECT = --package gym $(if $(DRY_RUN),--dry-run) $(if $(NO_COMMIT),--no-commit)

promote:
	@scripts/promote-release.sh $(ARGS)

promote-beta promote-rc:
	@scripts/promote-release.sh channel $(subst promote-,,$@) $(PROMOTE_SELECT)

promote-open-line:
	@scripts/promote-release.sh open-line $(LINE) $(PROMOTE_SELECT)
