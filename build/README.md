# Build Directory

The build directory is used to house all the build files and assets for your application. 

The structure is:

* bin - Output directory
* darwin - macOS specific files

## Mac

The `darwin` directory holds files specific to Mac builds.
These may be customised and used as part of the build. To return these files to the default state, simply delete them
and
build with `wails build`.

The directory contains the following files:

- `Info.plist` - the main plist file used for Mac builds. It is used when building using `wails build`.
- `Info.dev.plist` - same as the main plist file but used when building using `wails dev`.

## Linux

Linux builds use the shared `appicon.png` icon and do not need a
platform directory. `make build-linux` (or `wails build -platform
linux/amd64`) produces the binary under `build/bin/`; the build host
must have the GTK/WebKit development libraries installed (see the CI
workflow for the exact packages).
