IBus Bamboo - Bộ gõ tiếng Việt cho Linux/BSD
===================================
[![GitHub release](https://img.shields.io/github/release/BambooEngine/ibus-bamboo.svg)](https://github.com/BambooEngine/ibus-bamboo/releases/latest)
[![License: GPL v3](https://img.shields.io/badge/License-GPL%20v3-blue.svg)](https://opensource.org/licenses/GPL-3.0)
[![contributions welcome](https://img.shields.io/badge/contributions-welcome-brightgreen.svg?style=flat)](https://github.com/BambooEngine/ibus-bamboo)

## Mục lục

- [Sơ lược tính năng](#sơ-lược-tính-năng)
- [Hướng dẫn cài đặt](#hướng-dẫn-cài-đặt)
	- [Cài bản build sẵn (copy file)](#cài-bản-build-sẵn-copy-file)
	- [Dành cho Ubuntu, Mint và các distro tương tự](#ubuntu-và-các-distro-tương-tự-ppa)
	- [Dành cho Arch Linux và các distro tương tự](#arch-linux-và-các-distro-tương-tự)
	- [NixOS](#nixos)
	- [Void Linux](#void-linux)
	- [Cài đặt từ OpenBuildService](#cài-đặt-từ-openbuildservice)
	- [Cài đặt từ mã nguồn](https://github.com/BambooEngine/ibus-bamboo/wiki/H%C6%B0%E1%BB%9Bng-d%E1%BA%ABn-c%C3%A0i-%C4%91%E1%BA%B7t-t%E1%BB%AB-m%C3%A3-ngu%E1%BB%93n)
- [Hướng dẫn sử dụng](#hướng-dẫn-sử-dụng)
- [Báo lỗi](#báo-lỗi)
- [Giấy phép](#giấy-phép)

## Sơ lược tính năng
* Hỗ trợ tất cả các bảng mã phổ biến:
  * Unicode, TCVN (ABC)
  * VIQR, VNI, VPS, VISCII, BK HCM1, BK HCM2,…
  * Unicode UTF-8, Unicode NCR - for Web editors.
* Các kiểu gõ thông dụng:
  * Telex, Telex W, Telex 2, Telex + VNI + VIQR
  * VNI, VIQR, Microsoft layout
* Nhiều tính năng hữu ích, dễ dàng tùy chỉnh:
  * Kiểm tra chính tả (sử dụng từ điển/luật ghép vần)
  * Dấu thanh chuẩn và dấu thanh kiểu mới
  * Bỏ dấu tự do, Gõ tắt,...
  * 2666 emojis từ [emojiOne](https://github.com/joypixels/emojione)
* Sử dụng phím tắt <kbd>Shift</kbd>+<kbd>~</kbd> để loại trừ ứng dụng không dùng bộ gõ, chuyển qua lại giữa các chế độ gõ:
  	* Pre-edit (default)
  	* Surrounding text, IBus ForwardKeyEvent,...
   ![ibus-bamboo](https://github.com/BambooEngine/ibus-bamboo/raw/gh-resources/demo.gif)

## Hướng dẫn cài đặt
### Cài bản build sẵn (copy file)

Bản build sẵn có các bản sửa mới nhất (gõ nhanh, Wayland, GNOME 50, Firefox, VS Code...). Một bản dùng chung cho X11 và Wayland, chạy trên Ubuntu 22.04, 24.04, 26.04 và các distro x86_64 có glibc từ 2.34 trở lên (Debian 12, Linux Mint 21, Fedora 35...). Cần đã cài `ibus` và `curl`. Có thể tải tay file mới nhất ở trang [Releases](https://github.com/vupn0712/ibus-bamboo/releases/latest).

**Cách 1: cài đầy đủ** (máy chưa có ibus-bamboo, hoặc muốn ghi đè bản cũ)

```sh
url=$(curl -fsSL https://api.github.com/repos/vupn0712/ibus-bamboo/releases/latest | grep -o 'https://[^"]*/ibus-bamboo-[^"/]*-x86_64\.tar\.gz')
wget -O ibus-bamboo.tar.gz "$url"
tar -xzf ibus-bamboo.tar.gz
ibus exit
sudo ./ibus-bamboo/install.sh
ibus-daemon -drx
# Đặt ibus-bamboo làm bộ gõ mặc định (GNOME)
gsettings set org.gnome.desktop.input-sources sources "[('xkb', 'us'), ('ibus', 'Bamboo')]"
```

**Cách 2: chỉ thay file engine** (đã cài ibus-bamboo từ PPA hoặc apt)

```sh
url=$(curl -fsSL https://api.github.com/repos/vupn0712/ibus-bamboo/releases/latest | grep -o 'https://[^"]*/ibus-engine-bamboo-[^"/]*-x86_64')
wget -O ibus-engine-bamboo-linux-amd64 "$url"
ibus exit
sudo install -m 755 ibus-engine-bamboo-linux-amd64 /usr/lib/ibus-bamboo/ibus-engine-bamboo
sudo apt-mark hold ibus-bamboo   # để 'apt upgrade' không ghi đè lại bản cũ
ibus-daemon -drx
```

Kiểm tra bản đang dùng: `/usr/lib/ibus-bamboo/ibus-engine-bamboo -version`.

`ibus exit` tắt IBus trước khi thay file, `ibus-daemon -drx` khởi động lại IBus với engine mới. Nếu `ibus exit` báo `Can't connect to IBus` (IBus chưa chạy) thì bỏ qua.

Gỡ: với cách 1 chạy `sudo ./ibus-bamboo/install.sh uninstall`. Với cách 2 quay về bản của apt bằng `sudo apt-mark unhold ibus-bamboo && sudo apt install --reinstall ibus-bamboo`.

Chế độ gõ trên Wayland: chế độ 1 (Pre-edit) chạy tốt nhất. Các chế độ 2–5 đã chạy được trên GNOME Wayland với ứng dụng GTK, Qt và Firefox.

Chrome, Facebook và các ứng dụng Electron (VS Code...): chế độ 1 luôn có gạch chân dưới chữ đang gõ vì Chrome tự vẽ, tuỳ chọn "Ẩn gạch chân" không có tác dụng. Muốn không có gạch chân thì dùng chế độ 3: đứng trong ứng dụng, bấm <kbd>Shift</kbd>+<kbd>~</kbd> rồi chọn 3.

### Ubuntu và các distro tương tự (PPA)

Gói trên PPA (cũng như các gói AUR, Nixpkgs, Void, OpenBuildService bên dưới) là bản gốc của upstream, chưa có các bản sửa ở [bản build sẵn](#cài-bản-build-sẵn-copy-file). Có thể cài từ PPA rồi thay file engine theo cách 2 ở trên.

```sh
sudo add-apt-repository ppa:bamboo-engine/ibus-bamboo
sudo apt-get update
sudo apt-get install ibus ibus-bamboo --install-recommends
ibus restart
# Đặt ibus-bamboo làm bộ gõ mặc định
env DCONF_PROFILE=ibus dconf write /desktop/ibus/general/preload-engines "['BambooUs', 'Bamboo']" && gsettings set org.gnome.desktop.input-sources sources "[('xkb', 'us'), ('ibus', 'Bamboo')]"
```

### Arch Linux và các distro tương tự
`ibus-bamboo` hiện đã có mặt trên [AUR](https://aur.archlinux.org/packages/ibus-bamboo). Đừng quên để lại 1 vote cho các maintainer để 1 ngày không xa nó được vào kho repo chính thức của Arch nhé!

### NixOS

#### Nixpkgs

`ibus-bamboo` đã có mặt trên repo chính của Nixpkgs. Để cài đặt hãy chắc chắn rằng code sau đã có trong file cấu hình NixOS của bạn.

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

Nếu bạn không thích sử dụng package từ Nixpkgs, bạn có thể sử dụng bản mới nhất flake từ repo ibus-bamboo. Lưu ý rằng phương pháp này chỉ hoạt động với flake.

Đầu tiên hãy chắc chắn rằng bạn đã thêm repo path vào trong nixos flake của bạn.

Code ví dụ ở `flake.nix`:
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
```

Tiếp theo bạn hãy tạo biến và thêm nó vào `ibus.engines`

Code ví dụ ở `input-method.nix`:
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

Bước cuối cùng là cập nhập lại flake và chuyển đổi hệ thống sang cấu hình mới là xong.

### Void Linux
`ibus-bamboo` đã có mặt trên repo chính của Void Linux. Các bạn có thể cài đặt trực tiếp.

```sh
sudo xbps-install -S ibus-bamboo
```

### Cài đặt từ OpenBuildService
[![OpenBuildService](https://github.com/BambooEngine/ibus-bamboo/raw/gh-resources/obs.png)](https://software.opensuse.org//download.html?project=home%3Alamlng&package=ibus-bamboo)

## Hướng dẫn sử dụng
Điểm khác biệt giữa `ibus-bamboo` và các bộ gõ khác là `ibus-bamboo` cung cấp nhiều chế độ gõ khác nhau (1 chế độ gõ có gạch chân và 5 chế độ gõ không gạch chân; tránh nhầm lẫn **chế độ gõ** với **kiểu gõ**, các kiểu gõ bao gồm `telex`, `vni`, ...).

Để chuyển đổi giữa các chế độ gõ, chỉ cần nhấn vào một khung nhập liệu (một cái hộp để nhập văn bản) nào đó, sau đó nhấn tổ hợp <kbd>Shift</kbd>+<kbd>~</kbd>, một bảng với những chế độ gõ hiện có sẽ xuất hiện, bạn chỉ cần nhấn phím số tương ứng để lựa chọn.

**Một số lưu ý:**
- Một ứng dụng có thể hoạt động tốt với chế độ gõ này trong khi không hoạt động tốt với chế độ gõ khác.
- Các chế độ gõ được lưu riêng biệt cho mỗi phần mềm (`firefox` có thể đang dùng chế độ 3, trong khi `libreoffice` thì lại dùng chế độ 2).
- Bạn có thể dùng chế độ `Thêm vào danh sách loại trừ` để không gõ tiếng Việt trong một chương trình nào đó.
- Để gõ ký tự `~` hãy nhấn tổ hợp <kbd>Shift</kbd>+<kbd>~</kbd> 2 lần.
- Hỗ trợ Wayland trong IBus hiện chưa tốt lắm. Để có trải nghiệm gõ phím tốt hơn, hãy sử dụng Xorg.

## Báo lỗi
Trước khi báo lỗi vui lòng đọc [những vấn đề thường gặp](https://github.com/BambooEngine/ibus-bamboo/wiki/C%C3%A1c-v%E1%BA%A5n-%C4%91%E1%BB%81-th%C6%B0%E1%BB%9Dng-g%E1%BA%B7p) và tìm vấn đề của mình ở trong đó.

Nếu trang phía trên không giải quyết vấn đề của bạn, vui lòng [báo lỗi tại đây](https://github.com/BambooEngine/ibus-bamboo/issues)

## Đóng góp cho dự án

Nếu bạn muốn hiểu thêm về dự án có thể xem thêm ở file này. [HACKING.md](./docs/HACKING.adoc)

Đừng ngần ngại nếu bạn có 1 Pull Request hữu dụng. Hãy gửi lại nếu bạn muốn đóng góp cho dự án.

## Giấy phép
ibus-bamboo là phần mềm tự do nguồn mở, được phát hành dưới các quy định ghi trong Giấy phép Công cộng GNU (GNU General Public License v3.0).
