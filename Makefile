LIBS := core polylog blobs flags

.PHONY: test build tidy vet all $(LIBS)

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
