FROM golang:1.27.3-alpine AS dependencies

WORKDIR /workspace/platform
COPY platform/go.mod platform/go.sum ./
COPY engine/kfp/ /workspace/engine/kfp/
RUN go mod download

FROM dependencies AS development

RUN go install github.com/air-verse/air@v1.63.0
COPY install/docker/air.toml /etc/loom/air.toml
COPY platform/ ./
CMD ["air", "-c", "/etc/loom/air.toml", "--", "-config=configs/config.yaml", "-cmd=api"]

FROM dependencies AS build

COPY platform/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/loom-platform ./cmd/platform

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=build /out/loom-platform /app/loom-platform
COPY platform/configs/config.yaml platform/configs/images.yaml /app/configs/

USER nonroot:nonroot
ENTRYPOINT ["/app/loom-platform"]
CMD ["-config=/app/configs/config.yaml", "-cmd=api"]
