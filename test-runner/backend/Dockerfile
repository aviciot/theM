FROM golang:latest AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /test-runner-backend .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /test-runner-backend /test-runner-backend
EXPOSE 8090
CMD ["/test-runner-backend"]
