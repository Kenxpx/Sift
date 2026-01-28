FROM golang:1.22.12-bookworm

WORKDIR /app
COPY . .

RUN go test ./... && go build -buildvcs=false -o /usr/local/bin/sift ./cmd/sift
