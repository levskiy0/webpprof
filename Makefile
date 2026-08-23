.PHONY: tidy verify vet test check

tidy:
	./scripts/modules.sh tidy

verify:
	./scripts/modules.sh verify

vet:
	./scripts/modules.sh vet

test:
	./scripts/modules.sh test

check: verify vet test
	node --check ui/app.js
	git diff --check
