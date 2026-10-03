import { describe, expect, it, vi } from "vitest";
import type { Work } from "../src/api";
import { LIBRARY_PAGE_SIZE, LibraryPagination, type LibraryPage, type LibraryQuery } from "../src/libraryPagination";

const query: LibraryQuery = { lib: 7, sort: "title", dir: "asc", filter: "all" };

function works(count: number, start = 1): Work[] {
  return Array.from({ length: count }, (_, index) => ({
    id: start + index,
    title: `Work ${start + index}`,
    author: null,
    subtitle: null,
    hasCover: false,
    editions: [],
  }));
}

function deferred() {
  let resolve!: (value: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<unknown>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

function requestParams(path: string) { return new URL(path, "https://libteca.test"); }

describe("LibraryPagination", () => {
  it("loads more than 200 works in explicitly bounded pages without eager requests", async () => {
    const library = works(401);
    const request = vi.fn(async (path: string) => {
      const params = requestParams(path).searchParams;
      const offset = Number(params.get("offset"));
      const limit = Number(params.get("limit"));
      expect(limit).toBe(LIBRARY_PAGE_SIZE);
      expect(params.get("sort")).toBe("title");
      expect(params.get("dir")).toBe("asc");
      expect(params.get("filter")).toBe("all");
      return library.slice(offset, offset + limit);
    });
    const pager = new LibraryPagination(query, request, () => {});

    await pager.refresh();
    expect(pager.snapshot().works).toEqual(library.slice(0, 200));
    expect(pager.snapshot().hasMore).toBe(true);
    expect(request).toHaveBeenCalledTimes(1);

    await pager.loadMore();
    expect(pager.snapshot().works).toEqual(library.slice(0, 400));
    expect(pager.snapshot().hasMore).toBe(true);
    expect(request).toHaveBeenCalledTimes(2);

    await pager.loadMore();
    expect(pager.snapshot().works).toEqual(library);
    expect(pager.snapshot().hasMore).toBe(false);
    await pager.loadMore();
    expect(request.mock.calls.map(([path]) => requestParams(path).searchParams.get("offset"))).toEqual(["0", "200", "400"]);
  });

  it("checks the next page before declaring an exact full final page complete", async () => {
    const library = works(400);
    const request = vi.fn(async (path: string) => {
      const offset = Number(requestParams(path).searchParams.get("offset"));
      return library.slice(offset, offset + LIBRARY_PAGE_SIZE);
    });
    const pager = new LibraryPagination(query, request, () => {});

    await pager.refresh();
    await pager.loadMore();
    expect(pager.snapshot().works).toHaveLength(400);
    expect(pager.snapshot().hasMore).toBe(true);
    await pager.loadMore();
    expect(pager.snapshot().works).toEqual(library);
    expect(pager.snapshot().hasMore).toBe(false);
    expect(request).toHaveBeenCalledTimes(3);
    await pager.loadMore();
    expect(request).toHaveBeenCalledTimes(3);
  });

  it("does not start a second request on repeated load-more clicks", async () => {
    const gate = deferred();
    const request = vi.fn(() => gate.promise);
    const pager = new LibraryPagination(query, request, () => {});
    const pending = pager.refresh();
    await pager.loadMore();
    await pager.loadMore();
    expect(request).toHaveBeenCalledTimes(1);
    expect(pager.snapshot().loading).toBe(true);
    gate.resolve(works(200));
    await pending;
    expect(pager.snapshot().loading).toBe(false);
  });

  it.each([
    { lib: 8 },
    { sort: "author" },
    { dir: "desc" },
    { filter: "finished" },
  ])("clears loaded items and aborts a pending page immediately for query change %j", async (change) => {
    const oldPage = deferred();
    const newPage = deferred();
    const signals: AbortSignal[] = [];
    const request = vi.fn((_path: string, signal: AbortSignal): Promise<unknown> => {
      signals.push(signal);
      if (signals.length === 1) return Promise.resolve(works(200));
      return signals.length === 2 ? oldPage.promise : newPage.promise;
    });
    const pager = new LibraryPagination(query, request, () => {});
    await pager.refresh();
    const oldPending = pager.loadMore();
    const replacement = { ...query, ...change };
    const newPending = pager.refresh(replacement);

    expect(signals[1].aborted).toBe(true);
    expect(pager.snapshot()).toEqual({ works: [], loaded: false, loading: true, hasMore: true, error: false });
    const url = requestParams(request.mock.calls[2][0]);
    expect(url.pathname).toBe(`/libraries/${replacement.lib}/works`);
    expect(url.searchParams.get("offset")).toBe("0");
    expect(url.searchParams.get("sort")).toBe(replacement.sort);
    expect(url.searchParams.get("dir")).toBe(replacement.dir);
    expect(url.searchParams.get("filter")).toBe(replacement.filter);

    newPage.resolve(works(1, 900));
    await newPending;
    oldPage.resolve(works(200, 201));
    await oldPending;
    expect(pager.snapshot().works).toEqual(works(1, 900));
    expect(pager.snapshot().hasMore).toBe(false);
    expect(pager.snapshot().error).toBe(false);
  });

  it("ignores an old failure while a new filter is still loading", async () => {
    const first = deferred();
    const second = deferred();
    const request = vi.fn()
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    const pager = new LibraryPagination(query, request, () => {});
    const oldPending = pager.refresh();
    const newPending = pager.refresh({ ...query, filter: "in_progress" });
    first.reject(new Error("old request failed"));
    await oldPending;
    expect(pager.snapshot()).toEqual({ works: [], loaded: false, loading: true, hasMore: true, error: false });
    second.resolve(works(1, 600));
    await newPending;
    expect(pager.snapshot().works).toEqual(works(1, 600));
  });

  it("resets pagination on refresh and ignores an old successful response that arrives first", async () => {
    const oldPage = deferred();
    const refreshedPage = deferred();
    const request = vi.fn()
      .mockResolvedValueOnce(works(200))
      .mockReturnValueOnce(oldPage.promise)
      .mockReturnValueOnce(refreshedPage.promise);
    const pager = new LibraryPagination(query, request, () => {});
    await pager.refresh();
    const oldPending = pager.loadMore();
    const newPending = pager.refresh();
    expect(pager.snapshot().works).toEqual([]);
    oldPage.resolve(works(200, 201));
    await oldPending;
    expect(pager.snapshot().works).toEqual([]);
    expect(pager.snapshot().loading).toBe(true);
    refreshedPage.resolve(works(2, 700));
    await newPending;
    expect(pager.snapshot().works).toEqual(works(2, 700));
    expect(request.mock.calls.map(([path]) => requestParams(path).searchParams.get("offset"))).toEqual(["0", "200", "0"]);
  });

  it.each([new Error("offline"), { error: "Server error (503)", status: 503 }])(
    "retains loaded cards and retries the same offset after a failed next page: %j",
    async (failure) => {
      const firstPage = works(200);
      const request = vi.fn()
        .mockResolvedValueOnce(firstPage);
      if (failure instanceof Error) request.mockRejectedValueOnce(failure);
      else request.mockResolvedValueOnce(failure);
      request.mockResolvedValueOnce(works(1, 201));
      const pager = new LibraryPagination(query, request, () => {});
      await pager.refresh();
      await pager.loadMore();
      expect(pager.snapshot()).toEqual({ works: firstPage, loaded: true, loading: false, hasMore: true, error: true });
      await pager.loadMore();
      expect(pager.snapshot()).toEqual({ works: works(201), loaded: true, loading: false, hasMore: false, error: false });
      expect(request.mock.calls.map(([path]) => requestParams(path).searchParams.get("offset"))).toEqual(["0", "200", "200"]);
    },
  );

  it("exposes an initial failure for retry without turning it into an empty library", async () => {
    const request = vi.fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(works(1));
    const pager = new LibraryPagination(query, request, () => {});
    await pager.refresh();
    expect(pager.snapshot()).toEqual({ works: [], loaded: true, loading: false, hasMore: true, error: true });
    await pager.loadMore();
    expect(pager.snapshot().works).toEqual(works(1));
    expect(pager.snapshot().error).toBe(false);
    expect(request.mock.calls.map(([path]) => requestParams(path).searchParams.get("offset"))).toEqual(["0", "0"]);
  });

  it("advances by the server page length while avoiding duplicate cards", async () => {
    const request = vi.fn()
      .mockResolvedValueOnce(works(200))
      .mockResolvedValueOnce(works(200, 200))
      .mockResolvedValueOnce(works(1, 400));
    const pager = new LibraryPagination(query, request, () => {});
    await pager.refresh();
    await pager.loadMore();
    expect(pager.snapshot().works).toHaveLength(399);
    await pager.loadMore();
    expect(pager.snapshot().works).toEqual(works(400));
    expect(request.mock.calls.map(([path]) => requestParams(path).searchParams.get("offset"))).toEqual(["0", "200", "400"]);
  });

  it("aborts on teardown and suppresses late state changes and more requests", async () => {
    const gate = deferred();
    const request = vi.fn((_path: string, _signal: AbortSignal) => gate.promise);
    const changes: LibraryPage[] = [];
    const pager = new LibraryPagination(query, request, (page) => changes.push(page));
    const pending = pager.refresh();
    pager.stop();
    expect(request.mock.calls[0][1].aborted).toBe(true);
    const count = changes.length;
    gate.resolve(works(1));
    await pending;
    await pager.loadMore();
    await pager.refresh();
    expect(changes).toHaveLength(count);
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("does not request a library before one is selected", async () => {
    const request = vi.fn();
    const pager = new LibraryPagination({ ...query, lib: 0 }, request, () => {});
    await pager.refresh();
    expect(pager.snapshot()).toEqual({ works: [], loaded: true, loading: false, hasMore: false, error: false });
    expect(request).not.toHaveBeenCalled();
    request.mockResolvedValueOnce([]);
    await pager.refresh(query);
    expect(request).toHaveBeenCalledTimes(1);
    expect(pager.snapshot().hasMore).toBe(false);
  });
});
