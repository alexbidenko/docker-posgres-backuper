FROM golang:1.21-alpine AS build

ENV GOPATH="/go/src"

WORKDIR /go/src/application

COPY controller .

RUN GOOS=linux go build -ldflags="-s -w" -o main .

FROM postgres:15-alpine

RUN apk add --no-cache tzdata
ENV TZ=Europe/Moscow

WORKDIR /go/app

COPY README.md .
COPY --from=build /go/src/application/main ./controller

ENTRYPOINT  ["./controller", "start"]
