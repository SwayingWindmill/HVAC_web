FROM eclipse-mosquitto:2.1.2-alpine AS builder

RUN apk add --no-cache gcc musl-dev openssl-dev mosquitto-dev cjson-dev

WORKDIR /src
COPY infra/telemetry/mqtt/crl-plugin/mosquitto_hvac_crl.c ./mosquitto_hvac_crl.c

RUN mkdir -p /out \
  && gcc -O2 -Wall -Wextra -Werror -fPIC -shared \
    -o /out/mosquitto_hvac_crl.so \
    /src/mosquitto_hvac_crl.c \
    -lcrypto -lcjson

FROM scratch AS plugin-export
COPY --from=builder /out/mosquitto_hvac_crl.so /mosquitto_hvac_crl.so

FROM eclipse-mosquitto:2.1.2-alpine
COPY --from=builder --chown=0:0 /out/mosquitto_hvac_crl.so /usr/lib/mosquitto_hvac_crl.so
RUN chmod 0755 /usr/lib/mosquitto_hvac_crl.so
