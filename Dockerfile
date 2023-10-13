FROM golang:1.21-alpine AS build

ENV GOPATH="/go/src"

WORKDIR /build

COPY . .

RUN GOOS=linux go build -ldflags="-s -w" -o main .

FROM postgres:15-alpine

RUN apk add --no-cache tzdata
ENV TZ=Europe/Moscow

WORKDIR /app

COPY README.md .
COPY --from=build /build/main ./controller

ENTRYPOINT  ["./controller", "start"]
