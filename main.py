import eel
eel.init("web")

import tkinter as tk
from tkinter import filedialog
import os
import stat
import shutil
import platform
import subprocess

MIN_FILE_SIZE = 1024
EXCLUDED_PATHS = ["/proc", "/dev", "/sys", "/run", "/var/lib/docker", "/var/log/lastlog", "/snap"]

class FileRecord:
    def __init__(self, path, size, depth):
        self.name = os.path.basename(path)
        if self.name == "":
            self.name = "/"
        self.full_path = os.path.abspath(path)
        self.size = size
        self.depth = depth

class DirRecord(FileRecord):
    def __init__(self, path, depth=0):
        super().__init__(path, 0, depth)
        self.contains = []
        
        if os.path.islink(self.full_path):
            return 
        
        try:
            entries = os.listdir(self.full_path)
        except Exception:
            return

        for f in entries:
            p = os.path.join(self.full_path, f)

            if p in EXCLUDED_PATHS:
                continue

            try:
                st = os.stat(p)
            except:
                continue

            if stat.S_ISDIR(st.st_mode):
                d = DirRecord(p, depth + 1)
                self.contains.append(d)
                self.size += d.size

            elif stat.S_ISREG(st.st_mode):
                if MIN_FILE_SIZE > 0 and st.st_size < MIN_FILE_SIZE:
                    continue

                fm = FileRecord(f, st.st_size, depth + 1)
                self.contains.append(fm)
                self.size += st.st_size

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
        model = DirRecord(path)
        tree = model.get_full_tree()
        if model.full_path == "/":
           tree["children"].append({
               "name": "[Free Disk Space]",
                "size": shutil.disk_usage("/").free,
                "is_folder": False,
                "is_free_space":True,
                "depth": 0
           })
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
