{
  description = "watchinator";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
    flake-utils = {
      url = "github:numtide/flake-utils";
    };
  };

  outputs = { self, nixpkgs, flake-utils }: 
  let
    version = "main";
    tag = version;
    commit = if (self ? rev) then self.rev else "dirty";
  in
  flake-utils.lib.eachDefaultSystem (system: 
    let
      pkgs = nixpkgs.legacyPackages.${system};
      
      goDrv = pkgs.buildGoModule {
        pname = "watchinator";
        inherit version;

        src = self;

        env.CGO_ENABLED = 0;
        ldflags = [
          "-X github.com/learnitall/watchinator/cmd.commit=${commit}"
          "-X github.com/learnitall/watchinator/cmd.tag=${tag}"
        ];
        vendorHash = "sha256-5Fo5JVJGpj+J7BvOgtCYiiPNm3eQHxMNvgh2uLpNEik=";

        # Linting is a check, not a build step. It also must not run in preBuild:
        # buildGoModule leaks that into the vendoring derivation, where the vendor
        # tree is still incomplete and the lint load fails.
        nativeCheckInputs = [ pkgs.golangci-lint ];
        preCheck = ''
          export HOME=$(pwd)
          golangci-lint run --config .golangci-lint.yaml --verbose
        '';

        meta = {
          description = "Subscribe to things on GitHub using custom filters";
          homepage = "https://github.com/learnitall/watchinator";
          license = pkgs.lib.licenses.mit;
          maintainers = [
            {
              name = "Ryan Drew";
              email = "learnitall0@gmail.com";
              github = "learnitall";
            }
          ];
        };
      };

      dockerImage = pkgs.dockerTools.buildImage {
        name = "watchinator";
        inherit tag;

        copyToRoot = pkgs.buildEnv {
          name = "image-root";
          paths = [ 
            goDrv
            pkgs.fakeNss
          ];
          pathsToLink = [
            "/bin"
            "/etc"
            "/var"
          ];
        };

        config = {
          Entrypoint = [ "/bin/watchinator" ];
          User = "nobody:nobody";
          WorkingDir = "/opt/watchinator";
        };
      };
    in {
      packages = rec {
        watchinator-image = dockerImage;
        watchinator = goDrv;
        default = watchinator;
      };
      apps = rec {
        watchinator = flake-utils.lib.mkApp {
          drv = self.packages.${system}.watchinator;
        };
        default = watchinator;
      };
      nixosModules = rec {
        watchinator = {
          imports = [ ./module.nix ];
        };
        default = watchinator;
      };
      devShells.default = pkgs.mkShell {
        packages = with pkgs; [
          go
          golangci-lint
        ];
      };
    }
  );
}
