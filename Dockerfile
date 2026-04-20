### Build stage
FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/betterautoheal .

### Runtime stage
# docker:cli ships docker + the compose plugin so `docker compose` works for
# the `compose` revive mode.
FROM docker:26-cli

COPY --from=build /out/betterautoheal /usr/local/bin/betterautoheal

ENTRYPOINT ["/usr/local/bin/betterautoheal"]
