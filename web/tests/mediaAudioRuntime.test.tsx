import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AudioPlayer } from "../src/players/audio";
import { EditionAudio } from "../src/players/editionAudio";
import { setToken, type EditionDetail } from "../src/api";
let root: HTMLDivElement;
beforeEach(()=>{setToken("");root=document.createElement("div");document.body.append(root);vi.spyOn(HTMLMediaElement.prototype,"play").mockResolvedValue();vi.spyOn(HTMLMediaElement.prototype,"pause").mockImplementation(()=>{});vi.stubGlobal("fetch",vi.fn(async()=>new Response("{}",{status:200})));});
afterEach(async()=>{await act(async()=>render(null,root));root.remove();vi.restoreAllMocks();vi.unstubAllGlobals();});
const edition=():EditionDetail=>({id:71,title:"Audio",format:"audio",duration:0,position:5,files:[{id:31,seq:1,duration:10,size:1},{id:32,seq:2,duration:0,size:1}],chapters:[]});
async function mount(e=edition()){const audioRef={current:null as HTMLAudioElement|null};const seekRef={current:null as ((n:number)=>void)|null};const onTime=vi.fn();await act(async()=>render(<EditionAudio edition={e} sessionToken="" audioRef={audioRef} seekRef={seekRef} onTime={onTime} onPlaying={vi.fn()} onProgress={vi.fn()} onEnded={vi.fn()}/>,root));return {audioRef,seekRef,onTime};}
it("keeps a valid known-prefix resume when a later duration is unknown",async()=>{const p=await mount();const a=root.querySelector("audio")!;await act(async()=>a.dispatchEvent(new Event("loadedmetadata")));expect(a.currentTime).toBe(5);expect(p.onTime).toHaveBeenLastCalledWith(5,0);});
it("blocks content-conflicted audio until an explicit new start",async()=>{const e=edition();e.resumeConflict=true;await mount(e);expect(root.querySelector("audio")).toBeNull();expect(root.querySelector('[role="alert"]')?.textContent).toContain("changed media");await act(async()=>root.querySelector("button")!.click());const a=root.querySelector("audio")!;await act(async()=>a.dispatchEvent(new Event("loadedmetadata")));expect(a.currentTime).toBe(0);});

it("fences every edition audio part and exposes replacement recovery",async()=>{
 const e=edition();e.generation="71:5";e.files[1].duration=20;await mount(e);
 const first=root.querySelector("audio")!;expect(first.getAttribute("src")).toContain("generation=71%3A5");
 await act(async()=>first.dispatchEvent(new Event("loadedmetadata")));await act(async()=>first.dispatchEvent(new Event("ended")));
 const second=root.querySelector("audio")!;expect(second.getAttribute("src")).toContain("/stream/32?generation=71%3A5");
 await act(async()=>second.dispatchEvent(new Event("error")));
 expect(root.querySelector('[role="alert"]')?.textContent).toContain("Reload current media");expect(root.querySelector("audio")).toBeNull();
});
it("pins primary audio generation across paused queue part changes",async()=>{
 const controller={current:null as {playAt:(index:number,offset:number)=>void}|null};
 await act(async()=>render(<AudioPlayer files={[{id:31,title:"one",duration:10,generation:"71:5"},{id:32,title:"two",duration:20,generation:"71:5"}]} header="Audio" controllerRef={controller}/>,root));
 const audio=root.querySelector("audio")!;expect(audio.src).toContain("/stream/31?generation=71%3A5");
 await act(async()=>audio.dispatchEvent(new Event("loadedmetadata")));await act(async()=>controller.current!.playAt(1,3));
 expect(audio.src).toContain("/stream/32?generation=71%3A5");
 await act(async()=>audio.dispatchEvent(new Event("error")));expect(root.querySelector('[role="alert"]')?.textContent).toContain("Reload current media");
});
