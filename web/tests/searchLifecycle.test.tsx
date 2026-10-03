import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SearchBox, SearchPage } from "../src/views/search";
import { api } from "../src/api";

vi.mock("../src/api", () => {
  const api = vi.fn();
  return { api, apiWithDeadline: api, media: (path: string) => path };
});

const request = vi.mocked(api);
let root: HTMLDivElement;

function deferred() {
  let resolve!: (value: unknown) => void;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}

function result(title: string) {
  return { results: [{ workId: 1, libraryId: 1, libraryType: "books", title, author: "", hasCover: false }] };
}

async function type(value: string) {
  await act(async () => {
    const input = root.querySelector("input")!;
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await vi.advanceTimersByTimeAsync(250);
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  request.mockReset();
  root = document.createElement("div");
  document.body.append(root);
});

afterEach(() => {
  act(() => render(null, root));
  root.remove();
  vi.useRealTimers();
});

describe("search lifecycle", () => {
  it("ignores an older result after a newer query resolves", async () => {
    const old = deferred(), current = deferred();
    request.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    await act(async () => render(<SearchBox />, root));
    await type("old");
    await type("new");
    await act(async () => current.resolve(result("New result")));
    await act(async () => old.resolve(result("Old result")));
    expect(root.textContent).toContain("New result");
    expect(root.textContent).not.toContain("Old result");
  });

  it("does not reopen after Escape while the request is pending", async () => {
    const pending = deferred();
    request.mockReturnValue(pending.promise);
    await act(async () => render(<SearchBox />, root));
    await type("book");
    await act(async () => root.querySelector("input")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    await act(async () => pending.resolve(result("Book result")));
    expect(root.textContent).not.toContain("Book result");
  });

  it("does not reopen after clicking outside while a request is pending", async () => {
    const pending = deferred();
    request.mockReturnValue(pending.promise);
    await act(async () => render(<SearchBox />, root));
    await type("book");
    await act(async () => document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    await act(async () => pending.resolve(result("Book result")));
    expect(root.textContent).not.toContain("Book result");
  });

  it("invalidates the old request immediately when the query is cleared", async () => {
    const pending = deferred();
    request.mockReturnValue(pending.promise);
    await act(async () => render(<SearchBox />, root));
    await type("book");
    await act(async () => {
      const input = root.querySelector("input")!;
      input.value = "";
      input.dispatchEvent(new Event("input", { bubbles: true }));
      pending.resolve(result("Book result"));
    });
    expect(root.textContent).not.toContain("Book result");
  });

  it("aborts an in-flight request on unmount", async () => {
    request.mockReturnValue(new Promise(() => {}));
    await act(async () => render(<SearchBox />, root));
    await type("book");
    const signal = request.mock.calls[0][1]?.signal;
    act(() => render(null, root));
    expect(signal?.aborted).toBe(true);
  });

  it("restarts a dismissed pending search when the input is focused again", async () => {
    request.mockReturnValueOnce(new Promise(() => {})).mockResolvedValue(result("Book result"));
    await act(async () => render(<SearchBox />, root));
    await type("book");
    await act(async () => document.body.dispatchEvent(new MouseEvent("mousedown", { bubbles: true })));
    await act(async () => {
      root.querySelector("input")!.dispatchEvent(new Event("focus"));
      await vi.advanceTimersByTimeAsync(250);
    });
    expect(request).toHaveBeenCalledTimes(2);
    expect(root.textContent).toContain("Book result");
  });

  it("keeps the latest search page when older results arrive late", async () => {
    const old = deferred(), current = deferred();
    request.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    await act(async () => render(<SearchPage q="old" />, root));
    await act(async () => render(<SearchPage q="new" />, root));
    await act(async () => current.resolve(result("New result")));
    await act(async () => old.resolve(result("Old result")));
    expect(root.textContent).toContain("New result");
    expect(root.textContent).not.toContain("Old result");
  });
});
