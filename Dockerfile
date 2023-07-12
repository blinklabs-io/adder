# go:1.19 on 20230712
FROM cgr.dev/chainguard/go@sha256:c52c640eaaa1c5032d9eaa25e81e8ab0b7543d0ab1e2c09a0baec98e28620c9c AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS snek
COPY --from=build /code/snek /bin/
ENTRYPOINT ["snek"]
