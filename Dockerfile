FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod ./
# COPY go.sum ./ # No dependencies yet
COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o docker-safe-sock .

FROM scratch

COPY --from=builder /app/docker-safe-sock /docker-safe-sock


# Default to listening on all interfaces in container
ENV DSS_ADDR=:2375
ENV DSS_SOCKET=/var/run/docker.sock
EXPOSE 2375

ENTRYPOINT ["/docker-safe-sock"]
