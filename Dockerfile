FROM golang:latest AS builder
WORKDIR /go/src/app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o torpedo ./cmd/torpedo/main.go

# Second stage: copy the Go binary into a scratch container
FROM scratch
COPY --from=builder /go/src/app/torpedo /go/bin/torpedo
CMD ["/go/bin/torpedo", "server"]
