FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy module files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o /analytics-service .

# Use a minimal alpine image for the final container
FROM alpine:latest

WORKDIR /
COPY --from=builder /analytics-service /analytics-service

EXPOSE 3000
CMD ["/analytics-service"]
