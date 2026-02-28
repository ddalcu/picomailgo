FROM golang:1.24-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /picomailgo ./cmd/picomailgo

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /picomailgo /picomailgo

EXPOSE 25 80 443 587 993
VOLUME ["/data"]

ENTRYPOINT ["/picomailgo"]
