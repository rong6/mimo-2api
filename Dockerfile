FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod ./
# COPY go.sum ./ # Uncomment if your project has a go.sum
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o mimo-2api .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/mimo-2api .

EXPOSE 8090
ENTRYPOINT ["/app/mimo-2api"]
