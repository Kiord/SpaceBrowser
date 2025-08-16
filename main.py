import eel
eel.init("web")

import tkinter as tk
from tkinter import filedialog
import os
import shutil
import platform
import subprocess
from system_profile import get_system_profile, scan_entry, print_system_profile


class FileRecord:
    COUNT = 0
    def __init__(self, path, size, depth):
        self.name = os.path.basename(path)
        if self.name == "":
            self.name = "/"
        self.full_path = os.path.abspath(path)
        self.size = size
        self.depth = depth
        FileRecord.COUNT += 1


class DirRecord(FileRecord):
    COUNT = 0
    SKIPPED_RECORDS = {}

    def __init__(self, profile, path, depth=0, reset=False):
        super().__init__(path, 0, depth)
        
        if reset:
            DirRecord.COUNT = 0
            FileRecord.COUNT = 0
            DirRecord.SKIPPED_RECORDS = {}

        DirRecord.COUNT += 1

        self.contains = []

        try:
            with os.scandir(self.full_path) as entries:
                for entry in entries:
                    
                    should_skip, reason, is_dir, st = scan_entry(profile, entry)

                    if should_skip:
                        if reason in DirRecord.SKIPPED_RECORDS:
                            DirRecord.SKIPPED_RECORDS[reason].append(entry.path)
                        else:
                            DirRecord.SKIPPED_RECORDS[reason] = [entry.path]
                        continue
                   
                    if is_dir:
                        d = DirRecord(profile, entry.path, depth + 1)
                        self.contains.append(d)
                        self.size += getattr(d, "size", 0)
                    else:
                        size_val = profile["helpers"]["on_disk_size"](st) if profile.get("on_disk_size_enabled") else st.st_size
                        fm = FileRecord(entry.path, size_val, depth + 1)
                        self.contains.append(fm)
                        self.size += int(size_val)

        except Exception:
            return  # skip unreadable directory

        self.contains.sort(key=lambda f: f.size, reverse=True)

    def get_full_tree(self):
        return {
            "name": self.name,
            "size": self.size,
            "is_folder": True,
            "is_free_space":False,
            "depth": self.depth,
            "full_path": self.full_path,
            "children": [child.get_full_tree() if isinstance(child, DirRecord) else {
                "name": child.name,
                "size": child.size,
                "is_folder": False,
                "is_free_space":False,
                "depth": child.depth
            } for child in self.contains]
        }

@eel.expose
def get_full_tree(path):
    try:
        if not os.path.isdir(path):
            return {"error": f"Invalid path ({path})"}
        print(f"SpaceBrowsing \"{path}\"...")
        profile = get_system_profile(path)
        print_system_profile(profile)
        
        model = DirRecord(profile, path, reset=True)
        for key, value in DirRecord.SKIPPED_RECORDS.items():
            print(f'Skipped {len(value)} entries for {key}')

        tree = model.get_full_tree()
        if model.full_path == "/":
           tree["children"].append({
               "name": "[Free Disk Space]",
                "size": shutil.disk_usage("/").free,
                "is_folder": False,
                "is_free_space":True,
                "depth": 0
           })
        tree["file_count"] = FileRecord.COUNT - DirRecord.COUNT
        tree["folder_count"] = DirRecord.COUNT
        print(f"Done !") 
        return tree
    except Exception as e:
        print(f"Error in get_full_tree: {e}")
        return {"error": str(e)}

@eel.expose
def pick_folder():
    root = tk.Tk()
    root.withdraw()
    folder = filedialog.askdirectory()
    return folder

@eel.expose
def open_in_file_browser(path):
    try:
        if platform.system() == "Windows":
            subprocess.run(["explorer", path])
        elif platform.system() == "Darwin":  # macOS
            subprocess.run(["open", path])
        else:  # Linux
            # Try common file managers in order
            for cmd in [["dolphin", path], ["xdg-open", path], ["nautilus", path], ["thunar", path]]:
                try:
                    subprocess.run(cmd)
                    break
                except FileNotFoundError:
                    continue
    except Exception as e:
        print("Failed to open browser:", e)


if __name__  == '__main__':

    
    print('Starting server at\nhttp://localhost:8000')
    eel.start("index.html", mode='chrome', port=8000)
