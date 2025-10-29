{
  pkgs,
  pkgs-unstable,
  gomod2nix,
  ...
}:
pkgs.mkShell {
  nativeBuildInputs =
    with pkgs;
    [
      # common tools
      bashInteractive
      bash-completion
      git
      pwgen
      fd
      ripgrep
      gettext

      entr

      # go
      pkgs-unstable.go # go 1.25
      gopls
      gotools
      gotestsum
      goose
      semgrep
      enumer
      gomod2nix


      litecli # sqlite TUI
      # pgcli
      # postgresql

      # nix language server supported by vim-coc and vscode extension vscode-nix-ide
      # https://github.com/nix-community/nixd/blob/main/nixd/docs/editor-setup.md#teach-your-editor-find-the-executable-and-setup-configurations
      nixd

      hclfmt
    ]
    ++ lib.optionals stdenv.isDarwin [
      # https://discourse.nixos.org/t/the-darwin-sdks-have-been-updated/55295
      # https://nixos.org/manual/nixpkgs/stable/#sec-darwin
      apple-sdk
    ];

  shellHook = ''
    if [ -z "$DIRENV_IN_ENVRC" ]; then
      source ${pkgs.bash-completion}/etc/profile.d/bash_completion.sh
    fi

    export REPO_TL=$(git rev-parse --show-toplevel)

    eval "$(go env | grep -E '^(GOCACHE|GOMODCACHE|GOPROXY|GOSUMDB|GOBIN)')"
    export GOCACHE
    export GOMODCACHE
    export GOPROXY
    export GOSUMDB
    export GOBIN
    unset GOWORK
  '';
}
