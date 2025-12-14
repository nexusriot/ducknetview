#!/bin/env bash

version=0.0.4

echo "building deb for ducknetview $version"

if ! type "dpkg-deb" > /dev/null; then
  echo "please install required build tools first"
fi

project="ducknetview_${version}_amd64"
folder_name="build/$project"
echo "crating $folder_name"
mkdir -p $folder_name
cp -r DEBIAN/ $folder_name
bin_dir="$folder_name/usr/bin"
mkdir -p $bin_dir
go build -ldflags "-linkmode external -extldflags -static" -o ducknetview cmd/ducknetview/main.go

mv ducknetview $bin_dir
sed -i "s/_version_/$version/g" $folder_name/DEBIAN/control

cd build/ && dpkg-deb --build -Z gzip --root-owner-group $project