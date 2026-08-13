{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.watchinator;

  # Must agree with the xdg.configFile target below. systemd does not expand `~`.
  configPath = "${config.xdg.configHome}/watchinator/config.yaml";

  # Exec* lines are not shell: quote with "..." and neutralize % specifiers and $
  # expansion. Mirrors nixpkgs' escapeSystemdExecArg (nixos/lib/utils.nix), which
  # lives in NixOS's utils rather than lib, so it is unreachable from here.
  escapeExecArg = arg: builtins.replaceStrings [ "%" "$" ] [ "%%" "$$" ] (builtins.toJSON arg);
  execStart = lib.concatMapStringsSep " " escapeExecArg (
    [ "${cfg.package}/bin/watchinator" "watch" "--config" configPath ] ++ cfg.extraArgs
  );
in
{
  # Fail loudly if this gets imported as a NixOS or nix-darwin module.
  _class = "homeManager";

  options.services.watchinator = {
    enable = lib.mkEnableOption "watchinator";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.watchinator;
      defaultText = lib.literalExpression "watchinator.packages.\${system}.watchinator";
      description = "the watchinator package";
    };
    config = lib.mkOption {
      default = "";
      type = lib.types.str;
      description = "configuration file content";
    };
    extraArgs = lib.mkOption {
      default = [ ];
      type = lib.types.listOf lib.types.str;
      example = [ "--verbose" "--log-json" ];
      description = "extra args to pass to watchinator";
    };
  };

  config = lib.mkIf cfg.enable {
    xdg.configFile."watchinator/config.yaml".text = cfg.config;

    systemd.user.services.watchinator = {
      Unit = {
        Description = "Subscribe to things on GitHub using custom filters";
        After = "network.target";
      };
      Service = {
        Type = "simple";
        ExecStart = execStart;
      };
      Install = {
        WantedBy = [ "default.target" ];
      };
    };
  };
}
