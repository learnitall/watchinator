{ config, pkgs, ... }:
let
  cfg = config.services.watchinator;
in
{
  options = {
    services.watchinator = {
      package = pkgs.lib.mkOption {
        defaultText = "pkgs.watchinator";
        type = pkgs.lib.types.package;
        description = "the watchinator package";
      };
      enable = pkgs.lib.mkOption {
        default = false;
        type = pkgs.lib.types.bool;
        description = "start the watchinator service";
      };
      config = pkgs.lib.mkOption {
        default = "";
        type = pkgs.lib.types.str;
        description = "configuration file content";
      };
      extraArgs = pkgs.lib.mkOption {
        default = "";
        type = pkgs.lib.types.str;
        description = "extra args to pass to watchinator";
      };
    };
  };
  config.home.file = pkgs.lib.mkIf cfg.enable {
    watchinator-config = {
      enable = true;
      text = "${cfg.config}";
      target = ".config/watchinator/config.yaml";
    };
  };
  config.systemd.user.services.watchinator = pkgs.lib.mkIf cfg.enable {
    Unit = {
      Description = "Subscribe to things on GitHub using custom filters";
      After = "network.target";
    };
    Service = {
      Type = "simple";
      ExecStart = "${cfg.package}/bin/watchinator watch --config ~/.config/watchinator/config.yaml ${cfg.extraArgs}";
    };
  };
}
