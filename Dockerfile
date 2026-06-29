# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.22

FROM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG BINARY=./cmd/gateway
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/ruleshift-server \
    ${BINARY}

FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S ruleshift \
    && adduser -S -D -H -u 10001 -G ruleshift ruleshift

COPY --from=build /out/ruleshift-server /usr/local/bin/ruleshift-server

USER ruleshift

EXPOSE 8080

ENV RULESHIFT_ADDR=:8080
ENV RULESHIFT_ENV=prod

ENTRYPOINT ["/usr/local/bin/ruleshift-server"]
