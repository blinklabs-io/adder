#!/usr/bin/env bash

docker run --rm -v "${PWD}:/local" openapitools/openapi-generator-cli generate -i /local/docs/swagger.yaml --git-user-id blinklabs-io --git-repo-id adder -g go -o /local/openapi -c /local/openapi-config.yml
make format golines
cd openapi && go mod tidy
