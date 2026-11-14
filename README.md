# Skopos Helm chart repository

This branch publishes the Helm chart index for GitHub Pages.

The signed OCI chart is the primary release artifact:

```sh
helm install skopos oci://ghcr.io/mrofisr/charts/skopos \
  --version 0.0.1-alpha \
  -f values.release.yaml
```

Do not edit `index.yaml` or the packaged charts by hand.
