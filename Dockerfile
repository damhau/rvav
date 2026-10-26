# One image for every rvav process: api, worker, sync, migrate.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rvav ./cmd/rvav \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rvav-vcsim ./cmd/rvav-vcsim

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rvav"]
CMD ["api"]
