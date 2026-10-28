# Build from the repository's root:
#   docker build -f examples/decommission/Dockerfile -t decommission-worker .
FROM python:3.12-slim
COPY --from=ghcr.io/astral-sh/uv:0.12.2 /uv /usr/local/bin/uv
ENV UV_LINK_MODE=copy UV_COMPILE_BYTECODE=1
WORKDIR /src
COPY sdk/python sdk/python
COPY examples/decommission examples/decommission
WORKDIR /src/examples/decommission
RUN uv sync --frozen --no-dev
USER nobody
CMD ["uv", "run", "--frozen", "--no-dev", "python", "-m", "decommission.worker"]
