FROM golang:1.25 AS builder

WORKDIR /workspace

COPY . .

ENV GOFLAGS=-mod=vendor

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

COPY --from=builder /out/migrate /migrate

ENTRYPOINT ["/migrate"]
