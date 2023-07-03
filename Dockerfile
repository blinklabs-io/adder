FROM cgr.dev/chainguard/go:1.19 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS snek
COPY --from=build /code/snek /bin/
ENTRYPOINT ["snek"]
