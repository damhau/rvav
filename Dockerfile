# One image for every rvav process: api, worker, sync, migrate. The last
# stage is the default; the vcsim stage is rvav-vcsim's image.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
# rvav with Go's FIPS 140-3 module, v1.0.0 (CMVP certificate #5247); RVAV_FIPS
# turns the mode on at run time (ADR-0060). off builds with Go's own release.
ARG GOFIPS140=v1.0.0
RUN CGO_ENABLED=0 GOFIPS140=$GOFIPS140 go build -trimpath -ldflags="-s -w" -o /out/rvav ./cmd/rvav \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rvav-vcsim ./cmd/rvav-vcsim

# The vCenter simulator with the GVA layout and rvav's fixes to it, for the
# compose stack and kind (deploy/README.md): docker build --target vcsim.
FROM gcr.io/distroless/static-debian12:nonroot AS vcsim
COPY --from=build /out/rvav-vcsim /usr/local/bin/
USER 65532:65532
EXPOSE 8989
ENTRYPOINT ["/usr/local/bin/rvav-vcsim"]
CMD ["-listen", "0.0.0.0:8989"]

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
# Numeric, so Kubernetes can check it isn't root (runAsNonRoot).
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rvav"]
CMD ["api"]
