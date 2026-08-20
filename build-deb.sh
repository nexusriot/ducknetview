#!/bin/env bash

version="$(sed -n 's/.*Version = "\(.*\)".*/\1/p' internal/version/version.go)"
arch="${1:-amd64}"

echo "building deb for ducknetview $version ($arch)"

if ! type "dpkg-deb" > /dev/null; then
  echo "please install required build tools first (dpkg-deb)"
  exit 1
fi

case "$arch" in
  amd64)  goarch="amd64" ;;
  arm64)  goarch="arm64" ;;
  *)      echo "unsupported architecture: $arch"; exit 1 ;;
esac

project="ducknetview_${version}_${arch}"
folder_name="build/$project"
echo "crating $folder_name"
mkdir -p $folder_name
cp -r DEBIAN/ $folder_name
bin_dir="$folder_name/usr/bin"
mkdir -p $bin_dir
if [ "$arch" = "$(go env GOARCH)" ]; then
  go build -ldflags "-linkmode external -extldflags -static" -o ducknetview cmd/ducknetview/main.go
else
  CGO_ENABLED=0 GOARCH=$goarch go build -ldflags "-s -w" -o ducknetview cmd/ducknetview/main.go
fi

mv ducknetview $bin_dir

# Ship the man page and shell completions alongside the binary.
man_dir="$folder_name/usr/share/man/man1"
mkdir -p "$man_dir"
gzip -9 -c man/ducknetview.1 > "$man_dir/ducknetview.1.gz"

bash_dir="$folder_name/usr/share/bash-completion/completions"
mkdir -p "$bash_dir"
cp completions/ducknetview.bash "$bash_dir/ducknetview"

zsh_dir="$folder_name/usr/share/zsh/vendor-completions"
mkdir -p "$zsh_dir"
cp completions/_ducknetview "$zsh_dir/_ducknetview"
sed -i "s/_version_/$version/g" $folder_name/DEBIAN/control
sed -i "s/Architecture: .*/Architecture: $arch/" $folder_name/DEBIAN/control

cd build/ && dpkg-deb --build -Z gzip --root-owner-group $project
