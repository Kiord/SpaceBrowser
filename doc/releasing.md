## Trigger a new release

Create and push a version tag. The release build derives its version from the tag:

```sh
git tag -a vX.X.X -m "Release vX.X.X"
git push origin vX.X.X
```

Release tags must use `vmajor.minor.patch`, optionally followed by `-alpha`,
`-beta`, or `-rc`, with an optional numeric ordinal from 0 to 29 (for example
`v0.9.0-alpha.2`). Other tag forms fail validation before packaging.

The application and asset filenames retain the tag version. Windows MSI uses
a separate numeric version: `major.minor.(patch * 100 + rank)`. The ranks are
alpha = 1, beta = 33, rc = 65, and final = 99. A numbered prerelease adds its
ordinal plus one to its channel's base rank. This preserves upgrade ordering
from alpha through final and into the next patch. Major and minor must be at
most 255, and patch at most 654, keeping the MSI build field below 65,536.
The release-version Go tests verify this mapping and its bounds in CI.

## Remove release

```sh
git push origin main
git push origin --delete refs/tags/vX.X.X
git tag -d vX.X.X
```
