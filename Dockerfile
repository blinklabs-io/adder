FROM golang:1.18 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS snek
COPY --from=build /code/snek /usr/local/bin/
ENTRYPOINT ["/usr/local/bin/snek"]
