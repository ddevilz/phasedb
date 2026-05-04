FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-X main.version=${VERSION}" -o phasedb ./cmd/phasedb

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/phasedb /usr/local/bin/phasedb
ENTRYPOINT ["phasedb"]
