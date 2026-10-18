# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder
WORKDIR /src

# Build version reported by the /metrics app_info gauge.
# docker build --build-arg VERSION=$(git describe --tags --always --dirty)
ARG VERSION=dev

COPY go.mod ./
COPY . .

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=${VERSION}" -o /out/twitch-room .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=builder /out/twitch-room /app/twitch-room

ENV PORT=8080
EXPOSE 8080

CMD ["/app/twitch-room"]
