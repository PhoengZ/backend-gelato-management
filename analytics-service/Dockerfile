# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the application statically
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o analytics-service ./cmd/api

# Final minimal stage
FROM alpine:3.20

WORKDIR /app

# Add required CA certificates and tzdata
RUN apk --no-cache add ca-certificates tzdata

# Create a non-root user for better security and stability
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

# Copy the binary from the builder stage
COPY --from=builder /app/analytics-service /app/analytics-service

# Use non-root user
USER appuser

EXPOSE 8080

CMD ["/app/analytics-service"]
