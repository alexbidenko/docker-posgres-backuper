FROM golang:1.21-alpine AS build

ENV GOPATH="/go/src"

WORKDIR /build

COPY . .

RUN GOOS=linux go build -ldflags="-s -w" -o main .

FROM postgres:15-alpine

RUN apk add --no-cache tzdata
ENV TZ=Europe/Moscow

WORKDIR /app

COPY --chown=postgres:postgres README.md .
COPY --from=build --chown=postgres:postgres /build/main ./controller

USER postgres

ENTRYPOINT  ["./controller", "start"]
