.PHONY: tidy deps verify vet test vuln check

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

vuln:
	./scripts/modules.sh vuln

check: deps verify vet test
	node --check ui/app.js
	git diff --check
