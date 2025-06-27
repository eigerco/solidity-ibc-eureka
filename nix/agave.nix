{ lib
, stdenv
, fetchFromGitHub
, fetchurl
, rustPlatform
, pkg-config
, openssl
, zlib
, protobuf
, rustfmt
, perl
, hidapi
, darwin
, solanaPkgs ? [
    "cargo-build-sbf"
    "cargo-test-sbf"
    "solana"
    "solana-bench-tps"
    "solana-faucet"
    "solana-gossip"
    "agave-install"
    "solana-keygen"
    "agave-ledger-tool"
    "solana-log-analyzer"
    "solana-net-shaper"
    "agave-validator"
    "solana-test-validator"
    "solana-genesis"
  ]
}:

let
  platformToolsVersion = "v1.48";
  agave-version = "2.2.17";

  # Determine platform-tools archive based on system
  platformToolsArchive =
    if stdenv.isDarwin && stdenv.isx86_64 then "platform-tools-osx-x86_64.tar.bz2"
    else if stdenv.isDarwin && stdenv.isAarch64 then "platform-tools-osx-aarch64.tar.bz2"
    else if stdenv.isLinux && stdenv.isx86_64 then "platform-tools-linux-x86_64.tar.bz2"
    else if stdenv.isLinux && stdenv.isAarch64 then "platform-tools-linux-aarch64.tar.bz2"
    else throw "Unsupported platform for Solana platform-tools";

  platformTools = fetchurl {
    url = "https://github.com/anza-xyz/platform-tools/releases/download/${platformToolsVersion}/${platformToolsArchive}";
    sha256 =
      if stdenv.isDarwin && stdenv.isx86_64 then "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
      else if stdenv.isDarwin && stdenv.isAarch64 then "sha256-eZ5M/O444icVXIP7IpT5b5SoQ9QuAcA1n7cSjiIW0t0="
      else if stdenv.isLinux && stdenv.isx86_64 then "sha256-vHeOPs7B7WptUJ/mVvyt7ue+MqfqAsbwAHM+xlN/tgQ="
      else if stdenv.isLinux && stdenv.isAarch64 then "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
      else throw "No hash for platform";
  };

  # Download SBF SDK from Agave releases
  sbfSdk = fetchurl {
    url = "https://github.com/anza-xyz/agave/releases/download/v${agave-version}/sbf-sdk.tar.bz2";
    sha256 = "18nh745djcnkbs0jz7bkaqrlwkbi5x28xdnr2lkgrpybwmdfg06s";
  };
in
rustPlatform.buildRustPackage rec {
  pname = "agave";
  version = agave-version;

  src = fetchFromGitHub {
    owner = "anza-xyz";
    repo = "agave";
    rev = "v${version}";
    hash = "sha256-Xbv00cfl40EctQhjIcysnkVze6aP5z2SKpzA2hWn54o=";
    fetchSubmodules = true;
  };

  cargoHash = "sha256-DEMbBkQPpeChmk9VtHq7asMrl5cgLYqNC/vGwrmdz3A=";

  cargoBuildFlags = builtins.map (n: "--bin=${n}") solanaPkgs;

  nativeBuildInputs = [
    pkg-config
    protobuf
    rustfmt
    perl
  ];

  buildInputs = [
    openssl
    zlib
  ] ++ lib.optionals stdenv.isLinux [
    hidapi
  ] ++ lib.optionals stdenv.isDarwin [
    darwin.apple_sdk.frameworks.Security or darwin.Security or null
    darwin.apple_sdk.frameworks.SystemConfiguration or darwin.SystemConfiguration or null
  ];

  postPatch = ''
    substituteInPlace scripts/cargo-install-all.sh \
      --replace './fetch-perf-libs.sh' 'echo "Skipping fetch-perf-libs in Nix build"'

    substituteInPlace scripts/cargo-install-all.sh \
      --replace '"$cargo" $maybeRustVersion install' 'echo "Skipping cargo install"'
  '';

  postInstall = ''
    # Extract platform-tools
    mkdir -p $out/bin
    tar -xjf ${platformTools} -C $out/bin/

    # Extract SBF SDK
    mkdir -p $out/sbf-sdk
    tar -xjf ${sbfSdk} -C $out/sbf-sdk/

    # The SBF SDK expects platform-tools to be in dependencies/platform-tools
    mkdir -p $out/sbf-sdk/dependencies
    ln -sf $out/bin $out/sbf-sdk/dependencies/platform-tools

    # Remove broken symlinks
    find $out/bin -type l ! -exec test -e {} \; -delete 2>/dev/null || true

    # Create rustup shim
    cat > $out/bin/rustup <<'EOF'
    #!/usr/bin/env bash

    case "$1" in
      "toolchain")
        case "$2" in
          "list")
            echo "stable-x86_64-unknown-linux-gnu (default)"
            echo "nightly-x86_64-unknown-linux-gnu"
            echo "solana"
            ;;
          *)
            exit 0
            ;;
        esac
        ;;
      "which")
        case "$2" in
          "rustc")
            which rustc
            ;;
          "cargo")
            which cargo
            ;;
          *)
            exit 0
            ;;
        esac
        ;;
      "default")
        echo "stable-x86_64-unknown-linux-gnu (default)"
        ;;
      "show")
        echo "Default host: x86_64-unknown-linux-gnu"
        echo "rustup home:  $HOME/.rustup"
        echo ""
        echo "stable-x86_64-unknown-linux-gnu (default)"
        echo "rustc 1.75.0 (fake rustup shim)"
        ;;
      "+nightly"|"+stable")
        shift
        exec "$@"
        ;;
      "+solana")
        shift
        # Setup Solana toolchain environment
        export PATH="$out/bin/rust/bin:$out/bin/llvm/bin:$PATH"
        export RUSTC="$out/bin/rust/bin/rustc"
        export CARGO="$out/bin/rust/bin/cargo"
        exec "$@"
        ;;
      *)
        exit 0
        ;;
    esac
    EOF

    chmod +x $out/bin/rustup

    # Create environment setup script
    cat > $out/bin/agave-env <<EOF
    # Always export environment variables
    export BPF_SDK_PATH="$out/sbf-sdk"
    export SBF_SDK_PATH="$out/sbf-sdk"
    export CARGO_BUILD_SBF_SDK="$out/sbf-sdk"
    export PATH="$out/bin:\$PATH"
    
    # Don't set RUSTC/CARGO directly - let rustup shim handle it
    # But we need the rustup shim to work
    export RUSTUP="$out/bin/rustup"

    # Setup cache symlinks for cargo-build-sbf
    PLATFORM_TOOLS_VERSION="v1.48"
    CACHE_DIR="\$HOME/.cache/solana/\$PLATFORM_TOOLS_VERSION/platform-tools"
    if [ ! -d "\$CACHE_DIR" ]; then
      echo "Setting up Solana platform-tools cache..."
      mkdir -p "\$CACHE_DIR"
      ln -sf "$out/bin/rust" "\$CACHE_DIR/rust"
      ln -sf "$out/bin/llvm" "\$CACHE_DIR/llvm"
      echo "\$PLATFORM_TOOLS_VERSION" > "\$CACHE_DIR/.version"
    fi
    
    # Also setup SBF SDK cache
    SBF_CACHE_DIR="\$HOME/.cache/solana/v${version}/sbf-sdk"
    if [ ! -d "\$SBF_CACHE_DIR" ]; then
      echo "Setting up Solana SBF SDK cache..."
      mkdir -p "\$(dirname "\$SBF_CACHE_DIR")"
      ln -sf "$out/sbf-sdk" "\$SBF_CACHE_DIR"
    fi
    EOF

    chmod +x $out/bin/agave-env
  '';

  doCheck = false;

  meta = with lib; {
    description = "Solana validator implementation maintained by Anza";
    homepage = "https://github.com/anza-xyz/agave";
    license = licenses.asl20;
    platforms = platforms.unix;
  };
}
