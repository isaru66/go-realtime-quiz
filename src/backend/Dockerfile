FROM golang:1.27.3-alpine AS build
WORKDIR /build
COPY src/backend/go.mod src/backend/go.sum ./
RUN go mod download
COPY src/backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /server /app/server
COPY docs/protocol-v1.schema.json /app/protocol-v1.schema.json
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/server"]
