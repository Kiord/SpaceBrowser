import os
import sys
import platform
import shutil
import tempfile
import ctypes
from multiprocessing import cpu_count
import stat
import subprocess

def get_mount_root(path: str) -> str:
    """Return the filesystem root / drive for a given path."""
    if os.name == "nt":
        drive, _ = os.path.splitdrive(os.path.abspath(path))
        return (drive or "C:") + "\\"
    path = os.path.abspath(path)
    last = None
    while path and path != last:
        if os.path.ismount(path):
            return path
        last = path
        path = os.path.dirname(path)
    return "/"

def is_hidden(path: str) -> bool:
    name = os.path.basename(os.path.abspath(path))
    if not name:
        return False
    if os.name == "nt":
        FILE_ATTRIBUTE_HIDDEN = 0x2
        FILE_ATTRIBUTE_SYSTEM = 0x4
        try:
            attrs = ctypes.windll.kernel32.GetFileAttributesW(str(path))
            if attrs == -1:
                return False
            return bool(attrs & (FILE_ATTRIBUTE_HIDDEN | FILE_ATTRIBUTE_SYSTEM))
        except Exception:
            return False
    # POSIX: dotfile
    return name.startswith('.')

def case_sensitive_fs(test_dir: str = None) -> bool:
    # Probe if the FS is case-sensitive by writing a temp file
    try:
        base = test_dir or tempfile.gettempdir()
        with tempfile.NamedTemporaryFile(prefix="CsTest", dir=base, delete=False) as f:
            p1 = f.name
        p2 = p1[:-1] + ("X" if p1[-1].lower() != "x" else "Y")
        os.rename(p1, p2)  # should succeed regardless
        exists_lower = os.path.exists(p2.lower())
        os.remove(p2)
        return not exists_lower
    except Exception:
        return platform.system() != "Windows" and platform.system() != "Darwin"

def on_disk_size(stat_result) -> int:
    blocks = getattr(stat_result, "st_blocks", None)
    if blocks is not None:
        return int(blocks) * 512
    return int(stat_result.st_size)

def open_in_file_browser_cmds(path: str):
    sysname = platform.system()
    path = os.path.abspath(path)
    if sysname == "Windows":
        yield ["explorer", path]
    elif sysname == "Darwin":
        yield ["open", path]
    else:  # Linux / BSD
        for cmd in (["xdg-open", path], ["nautilus", path], ["dolphin", path], ["thunar", path]):
            yield cmd

def get_system_profile(selected_path: str = os.getcwd()) -> dict:
    sysname = platform.system()
    osname = os.name
    arch = platform.machine()
    python_version = sys.version.split()[0]

    # Excludes per OS (safe defaults; tweak per your app)
    excludes = set()
    if sysname == "Linux":
        excludes.update({
            "/proc", "/sys", "/dev", "/run", "/var/lib/docker",
            "/var/log/lastlog", "/snap"
        })
    elif sysname == "Darwin":
        excludes.update({
            "/System", "/private/var/vm", "/Volumes/MobileBackups",
            "/Library/Application Support/MobileSync/Backup"
        })
    elif sysname == "Windows":
        # Use env vars where possible; these may not all exist.
        windir = os.environ.get("WINDIR", r"C:\Windows")
        excludes.update({
            r"C:\$Recycle.Bin",
            r"C:\System Volume Information",
            os.path.join(windir, "WinSxS"),
            os.path.join(windir, "Temp"),
        })

    # Where to get disk usage
    mount_root = get_mount_root(selected_path)
    disk = shutil.disk_usage(mount_root)

    # UI / performance knobs
    logical_cores = max(1, cpu_count())
    # A sane upper bound to avoid FD explosions on huge trees
    max_workers = min(32, logical_cores * 4)

    # Browser/Eel mode preferences
    eel_modes = []
    if sysname == "Windows":
        eel_modes = ["edge", "chrome", None]
    elif sysname == "Darwin":
        eel_modes = ["chrome", None]  # 'open' uses default browser if needed
    else:
        eel_modes = ["chrome", None]

    profile = {
        # System facts
        "platform_system": sysname,           # 'Windows' | 'Darwin' | 'Linux' | ...
        "platform_osname": osname,            # 'nt' | 'posix'
        "platform_arch": arch,
        "python_version": python_version,

        # Filesystem characteristics
        "case_sensitive": case_sensitive_fs(),
        "path_sep": os.sep,
        "mount_root": mount_root,
        "disk_total": disk.total,
        "disk_used": disk.total - disk.free,
        "disk_free": disk.free,

        # Scanning policy
        "follow_symlinks": False,
        "min_file_size_bytes": 1024,          # you can override from UI
        "excluded_paths": sorted(excludes),
        "skip_hidden": False,

        # Size semantics
        "size_units": "binary",               # 'binary' (KiB/MiB) or 'decimal'
        "on_disk_size_enabled": True,         # use on_disk_size(stat) if True

        # Concurrency
        "max_workers": max_workers,

        # UI integration
        "eel_mode_preferences": eel_modes,    # try in order, fall back to None
    }

    # Attach helpers so you can call them from elsewhere
    profile["helpers"] = {
        "is_hidden": is_hidden,
        "open_in_file_browser_cmds": open_in_file_browser_cmds,
        "on_disk_size": on_disk_size,
        "get_mount_root": get_mount_root,
    }
    return profile


def _norm_abs(path):
    ap = os.path.abspath(path)
    return os.path.normcase(ap) if os.name == "nt" else ap

def scan_entry(profile: dict, entry):
    
    path = entry.path
    abspath = _norm_abs(path)

    # Symlinks
    try:
        if entry.is_symlink():
            return True, "symlink", None, None
    except Exception:
        return True, "symlink-check-error", None, None

    # Hidden
    try:
        if profile.get("skip_hidden") and profile["helpers"]["is_hidden"](abspath):
            return True, "hidden", None, None
    except Exception:
        pass

    # Excluded paths
    for ex in profile.get("excluded_paths", []):
        ex_abs = _norm_abs(ex)
        if abspath == ex_abs or abspath.startswith(ex_abs + os.sep):
            return True, "excluded", None, None

    # Is dir?
    try:
        is_dir = entry.is_dir(follow_symlinks=profile["follow_symlinks"])
    except Exception:
        return True, "type-unknown", None, None

    # File-specific checks
    if not is_dir:
        try:
            st = entry.stat(follow_symlinks=profile["follow_symlinks"])
        except Exception:
            return True, "stat-error", is_dir, None

        if not stat.S_ISREG(st.st_mode):
            return True, "non-regular", is_dir, st

        min_bytes = int(profile.get("min_file_size_bytes", 0) or 0)
        if min_bytes > 0 and st.st_size < min_bytes:
            return True, "min-size", is_dir, st

        return False, "", is_dir, st

    return False, "", is_dir, None


def open_in_file_browser_best_effort(path: str):
    """Open a folder in the platform file browser with graceful fallbacks."""
    profile = get_system_profile(path)
    for cmd in profile["helpers"]["open_in_file_browser_cmds"](path):
        try:
            subprocess.run(cmd, check=False)
            return
        except FileNotFoundError:
            continue
        except Exception:
            continue

def print_system_profile(profile: dict):
    """Pretty-print key information from a system profile."""
    print("=== System Profile ===")
    print(f"Platform:    {profile['platform_system']} ({profile['platform_osname']}, {profile['platform_arch']})")
    print(f"Python:      {profile['python_version']}")
    print(f"Case-Sensitive FS: {profile['case_sensitive']}")
    print(f"Mount Root:  {profile['mount_root']}")
    print(f"Disk Total:  {profile['disk_total']:,} bytes")
    print(f"Disk Used:   {profile['disk_used']:,} bytes")
    print(f"Disk Free:   {profile['disk_free']:,} bytes")
    print("--- Scanning Policy ---")
    print(f"Follow Symlinks:    {profile['follow_symlinks']}")
    print(f"Skip Hidden:        {profile['skip_hidden']}")
    print(f"Min File Size:      {profile['min_file_size_bytes']} bytes")
    print(f"Excluded Paths:     {len(profile['excluded_paths'])} paths")
    for ex in profile["excluded_paths"]:
        print(f"   - {ex}")
    print("--- Concurrency ---")
    print(f"Max Workers:        {profile['max_workers']}")
    print("--- UI Integration ---")
    print(f"Eel Mode Prefs:     {profile['eel_mode_preferences']}")
    print("====================")