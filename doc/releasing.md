## Trigger a new release

Create and push a version tag. The release build derives its version from the tag:

```sh
git tag -a vX.X.X -m "Release vX.X.X"
git push origin vX.X.X
```

## Remove release

```sh
git push origin main
git push origin --delete refs/tags/vX.X.X
git tag -d vX.X.X
```