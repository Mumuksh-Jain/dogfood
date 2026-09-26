# Stage 1: Build static Linux amd64 executable
FROM golang:1.27.1-alpine AS builder

WORKDIR /src

# Copy dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build statically with CGO disabled
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /bin/dogfood ./cmd/dogfood

# Stage 2: Minimal scratch runtime
FROM scratch

WORKDIR /

COPY --from=builder /bin/dogfood /dogfood
COPY official/fixtures.json /official/fixtures.json

EXPOSE 8080
VOLUME ["/data"]

ENV PORT=8080
ENV DB_PATH=/data/dogfood.db

ENTRYPOINT ["/dogfood"]
