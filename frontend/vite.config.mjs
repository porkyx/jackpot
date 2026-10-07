import {defineConfig} from "vite";

const common=["default-src 'self'","script-src 'self'","base-uri 'none'","object-src 'none'","frame-src 'none'","worker-src 'none'","form-action 'none'","font-src 'self'","media-src 'none'","img-src 'self' blob: https://dcimg1.dcinside.com https://dcimg4.dcinside.com https://dcimg5.dcinside.com https://dccon.dcinside.com https://image.dcinside.com"];
export const productionPolicy=[...common,"style-src 'self'","connect-src 'self'"].join("; ");
export const developmentPolicy=[...common,"style-src 'self' 'unsafe-inline'","connect-src 'self' ws://127.0.0.1:5173 ws://localhost:5173"].join("; ");

export default defineConfig(({command})=>({
 server:{host:"127.0.0.1",port:5173,strictPort:true},
 plugins:[{
  name:"jackpot-content-security",
  transformIndexHtml:{order:"post",handler:()=>[{
   tag:"meta",attrs:{"http-equiv":"Content-Security-Policy",content:command==="build"?productionPolicy:developmentPolicy},injectTo:"head-prepend",
  }]},
 }],
}));