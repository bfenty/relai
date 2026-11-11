# Stage 1: Build frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --production=false
COPY frontend/ ./
RUN npx tsc && npx vite build

# Stage 2: Build Go binary
FROM golang:1.25-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/frontend/dist ./internal/server/frontend/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /relai ./cmd/relai

# Stage 3: Final image
FROM alpine:3.19
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=go-builder /relai .
RUN mkdir -p /data
VOLUME ["/data"]

EXPOSE 8080
ENV RELAI_DB_PATH=/data/relai.db
ENV RELAI_HOST=0.0.0.0
ENV RELAI_PORT=8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["./relai"]
