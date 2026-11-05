# ---- build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Static binaries (CGO off) for api, the migration runner, and the seeder.
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -o /out/seed ./cmd/seed

# ---- runtime stage ----
FROM alpine:3.20 AS runtime
RUN adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=build /out/ /app/
# The migration runner reads ./migrations (file:// source).
COPY migrations /app/migrations
USER appuser
EXPOSE 8080
# CMD (not ENTRYPOINT) so compose `command:` can select a different binary
# (e.g. the one-shot migrate service runs /app/migrate up).
CMD ["/app/api"]
