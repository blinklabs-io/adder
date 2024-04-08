FROM ghcr.io/blinklabs-io/go:1.21.9-1 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS snek
COPY --from=build /code/snek /bin/
ENTRYPOINT ["snek"]
