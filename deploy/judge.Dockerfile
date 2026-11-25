FROM golang:1.24-bookworm

RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc g++ python3 openjdk-17-jdk-headless \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /work

COPY cmd/fifo/main_linux.go /tmp/nloj-fifo.go
RUN CGO_ENABLED=0 go build -trimpath -o /usr/local/bin/nloj-fifo /tmp/nloj-fifo.go \
 && rm /tmp/nloj-fifo.go
