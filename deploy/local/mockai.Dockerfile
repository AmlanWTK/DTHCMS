# Local mock AI and OCR service (CP04). Development only — the entrypoint refuses to
# start when DTHCMS_ENV is production.
#
# Build context is the repository root, so paths below are repo-relative.

FROM golang:1.23-alpine AS build
WORKDIR /src

# No third-party dependencies yet, so go.mod alone primes the module cache.
COPY backend/go.mod ./
RUN go mod download

COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/mockai ./tools/mockai

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mockai /mockai
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/mockai"]
