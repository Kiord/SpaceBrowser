import { SuggestFolders } from "./wailsjs/go/app/App.js";
import { byId } from "./dom.js";

export function initPathAutocomplete() {
  const input = byId("pathInput"), list = byId("pathSuggestions");
  let timer, generation = 0, paths = [], active = -1;
  const close = () => {
    ++generation;
    clearTimeout(timer);
    paths = []; active = -1;
    list.replaceChildren(); list.hidden = true;
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
  };
  const choose = index => {
    const path = paths[index];
    if (!path) return;
    input.value = path;
    close();
    input.focus();
    input.setSelectionRange(path.length, path.length);
    refreshSuggestions();
  };
  const highlight = () => {
    Array.from(list.children).forEach((item, index) => item.setAttribute("aria-selected", String(index === active)));
    if (active >= 0) {
      input.setAttribute("aria-activedescendant", `path-suggestion-${active}`);
      list.children[active].scrollIntoView({ block: "nearest" });
    }
  };
  const refreshSuggestions = async () => {
    close();
    const query = input.value, request = generation;
    if (!query) return;
    let result;
    try { result = await SuggestFolders(query); } catch { return; }
    if (request !== generation || input.value !== query) return;
    paths = result || [];
    paths.forEach((path, index) => {
      const item = document.createElement("div");
      item.id = `path-suggestion-${index}`;
      item.textContent = path;
      item.setAttribute("role", "option");
      item.setAttribute("aria-selected", "false");
      item.addEventListener("pointerdown", event => { event.preventDefault(); choose(index); });
      list.append(item);
    });
    list.hidden = !paths.length;
    input.setAttribute("aria-expanded", String(!!paths.length));
  };
  input.addEventListener("input", () => {
    close();
    timer = setTimeout(refreshSuggestions, 120);
  });
  input.addEventListener("blur", close);
  input.addEventListener("keydown", event => {
    if (event.isComposing) return;
    if (event.key === "Escape") { event.preventDefault(); close(); }
    else if (!paths.length) return;
    else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      active = event.key === "ArrowDown" ? (active + 1) % paths.length : (active <= 0 ? paths.length : active) - 1;
      highlight();
    } else if (event.key === "Tab" || (event.key === "Enter" && active >= 0)) {
      event.preventDefault(); choose(active >= 0 ? active : 0);
    }
  });
}
