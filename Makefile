LIBS := core polylog blobs flags
# Libs that depend on core — their require line gets bumped at publish time
DEPENDENTS := polylog blobs flags

.PHONY: test build tidy vet publish all $(LIBS)

## Run tests for all libs that have a go.mod
test:
	@for lib in $(LIBS); do \
		if [ -f libs/$$lib/go.mod ]; then \
			echo ">>> testing $$lib"; \
			cd libs/$$lib && go test ./... && cd ../..; \
		fi \
	done

## Build all libs that have a go.mod
build:
	@for lib in $(LIBS); do \
		if [ -f libs/$$lib/go.mod ]; then \
			echo ">>> building $$lib"; \
			cd libs/$$lib && go build ./... && cd ../..; \
		fi \
	done

## Run go vet for all libs
vet:
	@for lib in $(LIBS); do \
		if [ -f libs/$$lib/go.mod ]; then \
			echo ">>> vetting $$lib"; \
			cd libs/$$lib && go vet ./... && cd ../..; \
		fi \
	done

## Run go mod tidy for all libs
tidy:
	@for lib in $(LIBS); do \
		if [ -f libs/$$lib/go.mod ]; then \
			echo ">>> tidying $$lib"; \
			cd libs/$$lib && go mod tidy && cd ../..; \
		fi \
	done

## Run test for a single lib: make lib=core one
one:
	cd libs/$(lib) && go test ./...

## Publish all libs at a new version: make publish version=0.0.2
##   - Bumps the `require core` line in polylog/blobs/flags to the new version.
##   - Updates the workspace replace in go.work to match.
##   - Commits + pushes main, then tags libs/core first (dependents require it)
##     followed by the other three libs.
publish:
	@test -n "$(version)" || { echo "ERROR: version is required (e.g. make publish version=0.0.2)"; exit 1; }
	@case "$(version)" in v*) echo "ERROR: omit the leading 'v' (got '$(version)')"; exit 1;; esac
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "ERROR: working tree is not clean. Commit or stash first."; exit 1; \
	fi
	@echo ">>> Bumping require core to v$(version) in dependent libs"
	@for lib in $(DEPENDENTS); do \
		perl -i -pe 's{(github\.com/thescaffold/gox-packages/libs/core )v[0-9][\S]*}{$$1v$(version)}' libs/$$lib/go.mod; \
	done
	@echo ">>> Updating go.work replace to v$(version)"
	@perl -i -pe 's{(github\.com/thescaffold/gox-packages/libs/core )v[0-9][\S]*( =>)}{$$1v$(version)$$2}' go.work
	@if [ -n "$$(git status --porcelain)" ]; then \
		git add libs/*/go.mod go.work; \
		git commit -m "Release v$(version)"; \
		echo ">>> Committed version bump"; \
	fi
	@echo ">>> Pushing main"
	@git push origin HEAD
	@echo ">>> Tagging libs/core/v$(version) (dependents require it)"
	@git tag libs/core/v$(version)
	@git push origin libs/core/v$(version)
	@for lib in $(DEPENDENTS); do \
		echo ">>> Tagging libs/$$lib/v$(version)"; \
		git tag libs/$$lib/v$(version); \
		git push origin libs/$$lib/v$(version); \
	done
	@echo ">>> Published v$(version) for: $(LIBS)"

## Show which libs have been scaffolded
status:
	@echo "Scaffolded libs:"; \
	for lib in $(LIBS); do \
		if [ -f libs/$$lib/go.mod ]; then \
			echo "  [x] $$lib"; \
		else \
			echo "  [ ] $$lib"; \
		fi \
	done
