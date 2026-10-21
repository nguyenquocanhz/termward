# Termward website

A responsive, dependency-free static landing page for Termward. The site lives in `website/` so it stays separate from the React/Electron application in `app/`.

## Preview locally

From the repository root:

```bash
python -m http.server 8080 --directory website
```

Then open [http://localhost:8080](http://localhost:8080).

## Deploy with GitHub Pages

The `.github/workflows/pages.yml` workflow publishes this directory when changes are pushed to `main` and can also be run manually. In repository **Settings → Pages**, select **GitHub Actions** as the build and deployment source if Pages has not been configured yet.

## Product accuracy

The landing page uses screenshots already maintained under `docs/`. SFTP browsing and transfers are presented as planned work because the current project README lists the SFTP file browser on the roadmap. Update that copy when the feature ships. Current implemented positioning includes SSH host management, terminals, SSH key management, multi-host commands, and proactive server health checks.
