# Build stage. Floating golang:alpine tracks Go security fixes (see the Trivy fixes in analytics and gateway).
FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o notification-service ./cmd/api

# Final minimal stage
FROM alpine:3.22

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

COPY --from=builder /app/notification-service /app/notification-service

USER appuser

EXPOSE 8080

CMD ["/app/notification-service"]
