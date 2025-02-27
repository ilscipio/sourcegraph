# This Dockerfile tracks jaegertracing/all-in-one
# (https://github.com/jaegertracing/jaeger/blob/master/cmd/all-in-one/Dockerfile), but includes more
# debugging tools and runs as a non-root user. It requires JAEGER_VERSION to be set as an argument
# to build.
ARG JAEGER_VERSION
FROM jaegertracing/all-in-one:${JAEGER_VERSION} as base

FROM sourcegraph/alpine-3.14:166590_2022-08-11_7ebaa5ea4d88@sha256:f6b878c33efb48a151f112a996f3f71b59e3052288cade537bc6b538f0a2450e
USER root
RUN apk update
RUN apk --no-cache add bash curl 'apk-tools>=2.10.8-r0' 'krb5-libs>=1.18.4-r0'

COPY --from=base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=base /go/bin/all-in-one-linux /go/bin/all-in-one-linux
COPY --from=base /etc/jaeger/sampling_strategies.json /etc/jaeger/sampling_strategies.json

RUN adduser -S -u 10001 jaeger
RUN chown jaeger /go/bin/all-in-one-linux
RUN chown jaeger /etc/jaeger/sampling_strategies.json
USER jaeger

# Agent zipkin.thrift compact
EXPOSE 5775/udp

# Agent jaeger.thrift compact
EXPOSE 6831/udp

# Agent jaeger.thrift binary
EXPOSE 6832/udp

# Agent config HTTP
EXPOSE 5778

# Collector HTTP
EXPOSE 14268

# Collector gRPC
EXPOSE 14250

# Web HTTP
EXPOSE 16686

# Ensure the /tmp directory is chown'd to user jaeger
USER root
RUN mkdir -p /tmp
RUN chown -R jaeger /tmp
USER jaeger
VOLUME ["/tmp"]

# Used in order to reverse proxy the Jaeger UI
ENV QUERY_BASE_PATH="/-/debug/jaeger"

# Default configuration file for setting sampling strategies, we override the command in docker-compose
ENV SAMPLING_STRATEGIES_FILE=/etc/jaeger/sampling_strategies.json

ENTRYPOINT ["/go/bin/all-in-one-linux"]
CMD ["--sampling.strategies-file=/etc/jaeger/sampling_strategies.json"]

ARG COMMIT_SHA="unknown"
ARG DATE="unknown"
ARG VERSION="unknown"

LABEL org.opencontainers.image.revision=${COMMIT_SHA}
LABEL org.opencontainers.image.created=${DATE}
LABEL org.opencontainers.image.version=${VERSION}
LABEL org.opencontainers.image.url=https://sourcegraph.com/
LABEL org.opencontainers.image.source=https://github.com/sourcegraph/sourcegraph/
LABEL org.opencontainers.image.documentation=https://docs.sourcegraph.com/
LABEL com.sourcegraph.jaeger.version=${JAEGER_VERSION}
