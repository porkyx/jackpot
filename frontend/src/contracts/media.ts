// This whitelist is also enforced by Go's collector/frozen projection and CSP.
const origins=new Set(["dcimg5.dcinside.com","dcimg4.dcinside.com","dcimg1.dcinside.com","dccon.dcinside.com","image.dcinside.com"]);
export function isSafeDcMediaUrl(raw:string):boolean {
 if(!raw.startsWith("https://")||/[\u0000-\u0020\u007f\\]/.test(raw)||/%(?![0-9a-fA-F]{2})/.test(raw))return false;
 // Inspect the original authority as well: URL normalizes an explicit :443
 // away, and may normalize empty userinfo or escaped hostname characters.
 const authority=/^https:\/\/([^/?#]+)(?:[/?#]|$)/.exec(raw)?.[1];
 if(authority===undefined||!origins.has(authority.toLowerCase()))return false;
 try{const url=new URL(raw);return url.protocol==="https:"&&url.username===""&&url.password===""&&url.port===""&&origins.has(url.hostname)&&url.origin==="https://"+authority.toLowerCase();}catch{return false;}
}