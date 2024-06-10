FROM ghcr.io/blinklabs-io/go:1.21.11-1 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS adder
COPY --from=build /code/adder /bin/
ENTRYPOINT ["adder"]
