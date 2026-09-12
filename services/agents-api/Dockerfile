# Linux amd64 runtime. Build with make docker-build-agents-api.
# The context contains only the three independently built static executables.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e754765ad9e167b0677b41c617fd44afb7b9818a477f48f17bda08e12cfb98cb

COPY --chmod=0555 agents-api agents-api-migrate agents-api-device /usr/local/bin/
ENV AGENTS_API_ADDR=:8091
EXPOSE 8091
USER 65532:65532
CMD ["/usr/local/bin/agents-api"]
