# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/order-service ./cmd/server

# Final minimal stage
FROM alpine:3.20

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata \
    && addgroup -S appgroup \
    && adduser -S appuser -G appgroup

COPY --from=builder /out/order-service /app/order-service

USER appuser

EXPOSE 3003 50054

CMD ["/app/order-service"]
