.PHONY: tidy deps verify vet test check

tidy:
	./scripts/modules.sh tidy

verify:
	./scripts/modules.sh verify

deps:
	./scripts/check-core-dependencies.sh

vet:
	./scripts/modules.sh vet

test:
	./scripts/modules.sh test

check: deps verify vet test
	node --check ui/app.js
	git diff --check
