{
  description = "";
  inputs = {
    flake-utils.url = "github:numtide/flake-utils";

    # 1. Check Statuses of channels and hydra before updating
    #   - https://nixos.wiki/wiki/Nix_channels
    #   - https://status.nixos.org/
    # 2. Update links to the eval selected
    # 3. Document if revisions differs from hydra eval
    #   - Ex: slightly newer to pick up a patch

    #### Main stable release branch
    # https://hydra.nixos.org/jobset/nixos/release-25.05/evals
    nixpkgs = {
      # https://hydra.nixos.org/eval/1819210#tabs-inputs
      url = "github:NixOS/nixpkgs/5da4a26309e796daa7ffca72df93dbe53b8164c7";
      # url = "nixpkgs/nixos-25.05";
    };

    #### Unstable release branch
    # https://hydra.nixos.org/jobset/nixos/trunk-combined
    nixpkgs-unstable = {
      # https://hydra.nixos.org/eval/1819564#tabs-inputs
      url = "github:NixOS/nixpkgs/01f116e4df6a15f4ccdffb1bcd41096869fb385c";
      # url = "nixpkgs/nixos-unstable";
    };

    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs =
    {
      self,
      flake-utils,
      nixpkgs,
      nixpkgs-unstable,
      gomod2nix,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfree = true;
        };

        pkgs-unstable = import nixpkgs-unstable {
          inherit system;
          config.allowUnfree = true;
        };

        gomod2nixPkg = gomod2nix.packages.${system}.default;

      in
      rec {
        formatter = pkgs.nixfmt-rfc-style;
        apps = rec {
          default = go;
          go = {
            type = "app";
            program = "${pkgs-unstable.go}/bin/go";
          };
          gomod2nix = {
            type = "app";
            program = "${gomod2nixPkg}/bin/gomod2nix";
          };
        };
        packages = rec {
        };

        devShells.default = import ./shell.nix {
          inherit pkgs;
          inherit pkgs-unstable;
          gomod2nix = gomod2nixPkg;
        };
      }
    );
}
