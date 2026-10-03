import { useLayoutEffect, useMemo, useState } from "preact/hooks";
import { api, type Work } from "./api";

export const LIBRARY_PAGE_SIZE = 200;

export type LibraryQuery = {
  lib: number;
  sort: string;
  dir: string;
  filter: string;
};

export type LibraryPage = {
  works: Work[];
  loaded: boolean;
  loading: boolean;
  hasMore: boolean;
  error: boolean;
};

type RequestPage = (path: string, signal: AbortSignal) => Promise<unknown>;

function emptyPage(lib: number): LibraryPage {
  return { works: [], loaded: !lib, loading: false, hasMore: !!lib, error: false };
}

export class LibraryPagination {
  private query: LibraryQuery;
  private page: LibraryPage;
  private offset = 0;
  private generation = 0;
  private controller: AbortController | null = null;
  private stopped = false;

  constructor(
    query: LibraryQuery,
    private readonly request: RequestPage,
    private readonly onChange: (page: LibraryPage) => void,
  ) {
    this.query = { ...query };
    this.page = emptyPage(query.lib);
  }

  snapshot(): LibraryPage { return this.page; }

  async refresh(query: LibraryQuery = this.query): Promise<void> {
    if (this.stopped) return;
    this.generation += 1;
    this.controller?.abort();
    this.controller = null;
    this.query = { ...query };
    this.offset = 0;
    this.publish(emptyPage(query.lib));
    await this.loadMore();
  }

  async loadMore(): Promise<void> {
    if (this.stopped || this.page.loading || !this.page.hasMore || !this.query.lib) return;
    const generation = this.generation;
    const controller = new AbortController();
    this.controller = controller;
    const params = new URLSearchParams({
      sort: this.query.sort,
      dir: this.query.dir,
      filter: this.query.filter,
      limit: String(LIBRARY_PAGE_SIZE),
      offset: String(this.offset),
    });
    this.publish({ ...this.page, loading: true, error: false });
    try {
      const result = await this.request(`/libraries/${this.query.lib}/works?${params}`, controller.signal);
      if (this.stopped || generation !== this.generation || controller.signal.aborted) return;
      if (!Array.isArray(result) || result.length > LIBRARY_PAGE_SIZE) throw new Error("Invalid library page");
      const seen = new Set(this.page.works.map((work) => work.id));
      const works = [...this.page.works];
      for (const work of result as Work[]) {
        if (!seen.has(work.id)) {
          seen.add(work.id);
          works.push(work);
        }
      }
      this.offset += result.length;
      this.publish({ works, loaded: true, loading: false, hasMore: result.length === LIBRARY_PAGE_SIZE, error: false });
    } catch {
      if (this.stopped || generation !== this.generation || controller.signal.aborted) return;
      this.publish({ ...this.page, loaded: true, loading: false, error: true });
    } finally {
      if (this.controller === controller) this.controller = null;
    }
  }

  stop(): void {
    this.stopped = true;
    this.generation += 1;
    this.controller?.abort();
    this.controller = null;
  }

  private publish(page: LibraryPage): void {
    this.page = page;
    this.onChange(page);
  }
}

export function useLibraryPagination(query: LibraryQuery, refresh: number) {
  const [update, setUpdate] = useState<{ pager: LibraryPagination; page: LibraryPage } | null>(null);
  const pager = useMemo(() => {
    const next = new LibraryPagination(query, (path, signal) => api(path, { signal }),
      (page) => setUpdate({ pager: next, page }));
    return next;
  }, [query.lib, query.sort, query.dir, query.filter, refresh]);

  useLayoutEffect(() => {
    void pager.refresh();
    return () => pager.stop();
  }, [pager]);

  const page = update?.pager === pager ? update.page : pager.snapshot();
  return { ...page, loadMore: () => { void pager.loadMore(); } };
}
