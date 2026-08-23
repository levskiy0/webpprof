.PHONY: tidy deps docs verify vet test vuln check

tidy:
	./scripts/modules.sh tidy

verify:
	./scripts/modules.sh verify

deps:
	./scripts/check-core-dependencies.sh

docs:
	go run ./scripts/check-docs

vet:
	./scripts/modules.sh vet

test:
	./scripts/modules.sh test

vuln:
	./scripts/modules.sh vuln

check: deps docs verify vet test
	node --check ui/app.js
	git diff --check
