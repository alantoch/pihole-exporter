FROM debian:12-slim

COPY pihole-exporter /usr/local/bin/pihole-exporter

EXPOSE 9617
ENTRYPOINT ["pihole-exporter"]
