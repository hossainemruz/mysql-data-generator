FROM debian:stretch

RUN set -x \
  && apt-get update \
  && apt-get install -y --no-install-recommends apt-transport-https ca-certificates curl bzip2

COPY mysql-data-generator /bin/
RUN set -x \
  && chmod +x /bin/mysql-data-generator

ENTRYPOINT ["mysql-data-generator"]
