FROM golang:1.26.8-bookworm AS development
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

FROM development AS build
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/loadgen ./cmd/loadgen

FROM alpine:3.23 AS runtime
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/Daniel-Cpz/FlowForge" org.opencontainers.image.revision=$REVISION
RUN apk add --no-cache ca-certificates && addgroup -S flowforge && adduser -S -G flowforge flowforge
WORKDIR /app
COPY --from=build /out/ /app/
USER flowforge
EXPOSE 8080
CMD ["/app/api"]
