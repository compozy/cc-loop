MAGE ?= $(shell if command -v mage >/dev/null 2>&1 && mage -version >/dev/null 2>&1; then command -v mage; fi)

ifeq ($(strip $(MAGE)),)
MAGE_RUN = go run github.com/magefile/mage@v1.17.0
else
MAGE_RUN = $(MAGE)
endif

.PHONY: deps fmt vet lint test build release-check release-snapshot verify help

deps:
	@$(MAGE_RUN) deps

fmt:
	@$(MAGE_RUN) fmt

vet:
	@$(MAGE_RUN) vet

lint:
	@$(MAGE_RUN) lint

test:
	@$(MAGE_RUN) test

build:
	@$(MAGE_RUN) build

release-check:
	@$(MAGE_RUN) releaseCheck

release-snapshot:
	@$(MAGE_RUN) releaseSnapshot

verify:
	@$(MAGE_RUN) verify

help:
	@$(MAGE_RUN) -l
