# Publishing a new GitHub repository

The repo inside the ZIP has a single initial commit, but no remote origin.

1. Create a new **private** empty GitHub repository named `xiaomi-local-gateway`
   under your own account or organization (do not initialize with a README).
2. Replace placeholder `github.com/xiaomi-local-gateway/xiaomi-local-gateway`
   in `go.mod`, every Go import, and the `manifest.json` documentation/issue
   URLs with the actual repository URL.
3. Run `go mod tidy && gofmt -w cmd/ internal/ && go test ./...`.
4. Commit the path changes and publish:

```bash
git add .
git commit -m "chore: set GitHub module path"
git branch -M main
git remote add origin git@github.com:YOUR_OWNER/xiaomi-local-gateway.git
git push -u origin main
```

For HACS, install it as a custom **Integration** repository after publishing.
Until then, manually copy `custom_components/xiaomi_local_gateway` into HA.
Never commit `.env` or `config.json`, and do not make the repo public until
secrets/URLs have been reviewed.
