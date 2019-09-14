DOCKER_IMAGE_NAME?=$(shell pwd | xargs basename)
PROJECT_FILES=$(shell find . -type f -name '*.go')
PWD=$(shell pwd)

setup:
	go mod download
ifeq ("$(wildcard ./config.yml)","")
	@cp config.yml.sample config.yml
endif

docker:
	docker build --build-arg APP_NAME=${DOCKER_IMAGE_NAME} -t ${DOCKER_IMAGE_NAME} .
	docker run --rm -v ${PWD}/bin:/usr/${DOCKER_IMAGE_NAME}/bin ${DOCKER_IMAGE_NAME}

run: setup
	reset && $(shell time go run -race main.go --interval 1h --repeat 10 --max-parallel 3)

format:
ifeq ($(shell [ ! -x goimports ] || echo -n no),no)
	go get -u golang.org/x/tools/cmd/goimports
endif
	goimports -l -w -d ${PROJECT_FILES}
	gofmt -l -s -w ${PROJECT_FILES}

vet:
	go vet ${PROJECT_FILES}

lint:
ifeq ($(shell [ ! -x revive ] || echo -n no),no)
	go get -u github.com/mgechev/revive
endif
	revive -config revive.toml -formatter stylish ./...

sanitize:
	/bin/rm reports/*.csv
