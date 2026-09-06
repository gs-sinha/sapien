# Runtime image for the CI-use sapien binary (PLAN.md §33). goreleaser
# builds the sapien binary separately (CGO_ENABLED=0, see .goreleaser.yaml)
# and hands it to this Dockerfile via its build context; this file only
# copies it onto a distroless static base.
FROM gcr.io/distroless/static:nonroot

COPY sapien /usr/local/bin/sapien

ENTRYPOINT ["/usr/local/bin/sapien"]
CMD ["--help"]
