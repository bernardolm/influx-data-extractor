FROM golang:1.13-alpine AS base

ENV GO111MODULE on
ARG APP_NAME
WORKDIR /usr/${APP_NAME}

COPY . ./

RUN go mod download

VOLUME ["/bin"]

CMD CGO_ENABLED=0 GOOS=linux GARCH=amd64 go build -o bin/${APP_NAME} -ldflags '-s -w -extldflags "-static"'
