# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=${VERSION}" -o /out/twitch-room .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -H -s /sbin/nologin twitch
WORKDIR /app

COPY --from=builder /out/twitch-room /app/twitch-room
RUN chown twitch:twitch /app/twitch-room

USER twitch
ENV PORT=8080
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/health >/dev/null 2>&1 || exit 1

CMD ["/app/twitch-room"]