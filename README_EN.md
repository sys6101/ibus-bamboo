IBus Bamboo - An open source Vietnamese IME for IBus using Bamboo Engine
===================================
[![GitHub release](https://img.shields.io/github/release/BambooEngine/ibus-bamboo.svg)](https://github.com/BambooEngine/ibus-bamboo/releases/latest)
[![License: GPL v3](https://img.shields.io/badge/License-GPL%20v3-blue.svg)](https://opensource.org/licenses/GPL-3.0)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/BambooEngine/ibus-bamboo)

IBus Bamboo is a Vietnamese input method engine for IBus that translates key strokes into Vietnamese characters. For example, when you type `a`, an `a` will appear on the screen, but if one more `a` is typed, IBus Bamboo will replace the first `a` with the letter `â` according to [Telex](https://en.wikipedia.org/wiki/Telex_(input_method)) typing.

   ![ibus-bamboo](https://github.com/BambooEngine/ibus-bamboo/raw/gh-resources/demo.gif)

## Getting Started

- [Features](#features)
- [Installation](#installation)
	- [Prebuilt release (copy the files)](#prebuilt-release-copy-the-files)
	- [Ubuntu, Mint and derivatives](#ubuntu-and-derivatives-ppa)
	- [Arch Linux and derivatives](#arch-linux-and-derivatives)
	- [NixOS](#nixos)
	- [Void Linux](#void-linux)
	- [Install from OpenBuildService](#install-from-openbuildservice)
	- [Install from source](https://github.com/BambooEngine/ibus-bamboo/wiki/H%C6%B0%E1%BB%9Bng-d%E1%BA%ABn-c%C3%A0i-%C4%91%E1%BA%B7t-t%E1%BB%AB-m%C3%A3-ngu%E1%BB%93n)
- [Usage](#usage)
- [Bug reports](#bug-reports)
- [License](#license)

## Features
* Support many Vietnamese character sets/encodings:
  * Unicode, TCVN (ABC)
  * VIQR, VNI, VPS, VISCII, BK HCM1, BK HCM2,…
  * Unicode UTF-8, Unicode NCR - for Web editors.
* All popular typing methods:
  * Telex, Telex W, Telex 2, Telex + VNI + VIQR
  * VNI, VIQR, Microsoft layout
* Using shortcut <kbd>Shift</kbd>+<kbd>~</kbd> to switch between typing modes for an application or add it to the exclusion list:
  	* Pre-edit (default)
  	* Surrounding text, IBus ForwardKeyEvent,...
* Other useful futures, easy to use:
  * Spelling check (using dictionary/rules)
  * Use oà, uý (instead of òa, úy)
  * Free tone making, macro,...
  * 2666 emojis from [emojiOne](https://github.com/joypixels/emojione)

## Installation
### Prebuilt release (copy the files)

The prebuilt release has the latest fixes (fast typing, Wayland, GNOME 50, Firefox, VS Code...). One build works on both X11 and Wayland, on Ubuntu 22.04, 24.04, 26.04 and other x86_64 distributions with glibc 2.34 or later (Debian 12, Linux Mint 21, Fedora 35...). `ibus` and `curl` must be installed. The latest files can also be downloaded by hand from the [Releases](https://github.com/vupn0712/ibus-bamboo/releases/latest) page.

**Option 1: full install** (ibus-bamboo not installed yet, or to overwrite an older one)

```sh
url=$(curl -fsSL https://api.github.com/repos/vupn0712/ibus-bamboo/releases/latest | grep -o '"browser_download_url": *"[^"]*/ibus-bamboo-[^"]*-x86_64\.tar\.gz"' | cut -d'"' -f4)
wget -O ibus-bamboo.tar.gz "$url"
tar -xzf ibus-bamboo.tar.gz
ibus exit
sudo ./ibus-bamboo/install.sh
ibus-daemon -drx
# Make ibus-bamboo an input source (GNOME)
gsettings set org.gnome.desktop.input-sources sources "[('xkb', 'us'), ('ibus', 'Bamboo')]"
```

**Option 2: replace only the engine** (ibus-bamboo installed from the PPA or apt)

```sh
url=$(curl -fsSL https://api.github.com/repos/vupn0712/ibus-bamboo/releases/latest | grep -o '"browser_download_url": *"[^"]*/ibus-engine-bamboo-[^"]*-x86_64"' | cut -d'"' -f4)
wget -O ibus-engine-bamboo-linux-amd64 "$url"
ibus exit
sudo install -m 755 ibus-engine-bamboo-linux-amd64 /usr/lib/ibus-bamboo/ibus-engine-bamboo
sudo apt-mark hold ibus-bamboo   # so that 'apt upgrade' does not bring the old one back
ibus-daemon -drx
```

Check the version in use: `/usr/lib/ibus-bamboo/ibus-engine-bamboo -version`.

`ibus exit` stops IBus before the files are replaced, and `ibus-daemon -drx` starts it again with the new engine. If `ibus exit` says `Can't connect to IBus` (IBus is not running), ignore it.

To remove: with option 1 run `sudo ./ibus-bamboo/install.sh uninstall`. With option 2 go back to the apt version with `sudo apt-mark unhold ibus-bamboo && sudo apt install --reinstall ibus-bamboo`.

Input modes on Wayland: mode 1 (Pre-edit) works best. Modes 2–5 work on GNOME Wayland with GTK, Qt and Firefox applications.

Chrome, Facebook and Electron applications (VS Code...): mode 1 always underlines the text being typed, as Chrome draws the underline itself; "Hide underline" has no effect there. For no underline use mode 3: in the application, press <kbd>Shift</kbd>+<kbd>~</kbd> and choose 3.

### Ubuntu and derivatives (PPA)

The PPA package (like the AUR, Nixpkgs, Void and OpenBuildService packages below) is the original upstream build, without the fixes in the [prebuilt release](#prebuilt-release-copy-the-files). You can install it from the PPA and then replace the engine as in option 2 above.

```sh
sudo add-apt-repository ppa:bamboo-engine/ibus-bamboo
sudo apt-get update
sudo apt-get install ibus-bamboo
ibus restart
# Make ibus-bamboo your default input method, this will remove other existing input layouts
env DCONF_PROFILE=ibus dconf write /desktop/ibus/general/preload-engines "['xkb:us::eng', 'Bamboo']" && gsettings set org.gnome.desktop.input-sources sources "[('xkb', 'us'), ('ibus', 'Bamboo')]"
```

### Arch Linux and derivatives
`ibus-bamboo` is now available on the [AUR](https://aur.archlinux.org/packages/ibus-bamboo). Don't forget to leave a vote for the maintainers so that one day it can be included in the official Arch repository!

### NixOS

#### Nixpkgs

`ibus-bamboo` is available on the main Nixpkgs repo. Make sure your NixOS configuration must contain this code to install it.

```nix
{
 i18n.inputMethod = {
  enabled = "ibus";
  ibus.engines = with pkgs.ibus-engines; [
    bamboo
  ];
 };
}
```

#### Ibus-bamboo flake

If you don't like to use package from Nixpkgs, you can use latest version flake package from Ibus-bamboo repo. Note that this approach only work for flake.

First, make sure you have added repo path in your nixos flake config.

Example code at `flake.nix`
```nix
{
  inputs = {
    nixpkgs = {
      url = "github:nixos/nixpkgs/nixos-24.05";
    };

    ibus-bamboo = {
      url = "github:BambooEngine/ibus-bamboo";
    };
  };

  outputs = {
    self,
    nixpkgs,
    ibus-bamboo
  }@inputs:
  let
    inherit (self) outputs;

    system = "x86_64-linux";
  in
  {
    nixosConfigurations = {
      nixos = nixpkgs.lib.nixosSystem {
        specialArgs = { inherit inputs outputs system; };

        # Some nixos config
      };
    };
  }
}
}
```

Next, you need to declare a variable and add it to `ibus.engines`

Example code at `input-method.nix`
```nix
{ inputs, system, ... }:

let
  bamboo = inputs.ibus-bamboo.packages."${system}".default;
in
{
  i18n.inputMethod = {
    enabled = "ibus";
    ibus.engines = [
      bamboo
    ];
  };
}
```

Final step is update flake and switch your system to new config.

### Void Linux
`ibus-bamboo` is available on the main Void Linux repo. You can install it directly.

```sh
sudo xbps-install -S ibus-bamboo
```

### Install from OpenBuildService
[![OpenBuildService](https://github.com/BambooEngine/ibus-bamboo/raw/gh-resources/obs.png)](https://software.opensuse.org//download.html?project=home%3Alamlng&package=ibus-bamboo)

## Usage
The difference between `ibus-bamboo` and other input methods is that `ibus-bamboo` provides different typing modes (1 underlined and 5 non-underlined typing modes - don't confuse **typing mode** with **typing method**, typing methods are `telex`, `vni`, ...).

To switch between typing modes, simply click on an input box (a box to enter text), then press the combination <kbd>Shift</kbd>+<kbd>~</kbd>, a table with the available typing modes will appear, you just need to press the corresponding number key to select.

**Note:**
 - An app may work well with one typing mode while not working well with another.
 - Typing modes are saved separately for each software (`firefox` is probably using mode 5, while `libreoffice` is using mode 2).
 - You can use `Add to the exclusion list` mode to not type Vietnamese in a certain program.
 - To type the character `~`, press the combination <kbd>Shift</kbd>+<kbd>~</kbd> twice.
 - Support for Wayland in IBus is not yet ideal. For a better typing experience, please use Xorg.

## Bug reports
Before submitting a question or bug report, please ensure you have read through [these common issues](https://github.com/BambooEngine/ibus-bamboo/wiki/C%C3%A1c-v%E1%BA%A5n-%C4%91%E1%BB%81-th%C6%B0%E1%BB%9Dng-g%E1%BA%B7p) and see if you can resolve the problem on your own. If you still encounter issues after trying these steps, or you don't see something similar to your issue listed, please submit a bug report in the [Bamboo issue tracker](https://github.com/BambooEngine/ibus-bamboo/issues)

## License
IBus Bamboo is released under the GNU General Public License v3.0
