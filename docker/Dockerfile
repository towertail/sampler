# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG SHA=unknown
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags="-s -w -X github.com/towertail/sampler/internal/version.Version=${VERSION} -X github.com/towertail/sampler/internal/version.SHA=${SHA}" \
      -o /out/towertail-sampler ./cmd/sampler

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/towertail-sampler /usr/local/bin/towertail-sampler
ENTRYPOINT ["/usr/local/bin/towertail-sampler"]
CMD ["push", "--interval", "30s"]
