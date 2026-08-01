FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
RUN apt-get update && apt-get install -y --no-install-recommends protobuf-compiler && rm -rf /var/lib/apt/lists/*
RUN protoc -I . --include_imports --descriptor_set_out=/descriptor.pb examples/modules/xiangqi/xiangqi.proto
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /module ./examples/modules/runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /module /app/module
COPY --from=build /descriptor.pb /app/descriptor.pb
ENV RULESHIFT_MODULE_ID=xiangqi RULESHIFT_MODULE_VERSION=2.0.0
EXPOSE 50051
ENTRYPOINT ["/app/module"]
