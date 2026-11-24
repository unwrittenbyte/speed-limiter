# SPDX-License-Identifier: GPL-3.0-only
"""Black-box checks for a compiled companion (not Chrome integration)."""
import sys, subprocess, struct, json, socket, base64, http.client
p=subprocess.Popen([sys.argv[1]],stdin=subprocess.PIPE,stdout=subprocess.PIPE)
seq=0
def call(cmd,**kw):
 global seq
 seq+=1; data=json.dumps(dict(id=seq,command=cmd,**kw)).encode();p.stdin.write(struct.pack('<I',len(data))+data);p.stdin.flush(); prefix=p.stdout.read(4); assert len(prefix)==4; reply=json.loads(p.stdout.read(struct.unpack('<I',prefix)[0]));assert reply['id']==seq;return reply
try:
 assert call('hello')['state']['version']=='1.0.6'
 for v,u in [('0','MB/s'),('NaN','MB/s'),('1e2','KB/s'),('2','GB/s'),('1','wrong')]:assert not call('start',value=v,unit=u)['ok']
 r=call('start',value='2',unit='MB/s');assert r['ok']; port=r['state']['port'];auth=r['auth'];assert len(auth['password'])==43;assert r['state']['vpn_compat'] is False
 def send(headers=None,path='http://127.0.0.1:80/'):
  c=http.client.HTTPConnection('127.0.0.1',port,timeout=5);c.request('GET',path,headers=headers or {});res=c.getresponse();status=res.status;res.read();c.close();return status
 assert send()==407
 assert send({'Proxy-Authorization':'Basic invalid'})==407
 token=base64.b64encode(('csl:'+auth['password']).encode()).decode();assert send({'Proxy-Authorization':'Basic '+token})==502
 assert send({'Proxy-Authorization':'Basic '+token},'/__csl_status')==400
 assert send({'Proxy-Authorization':'Basic '+token},'http://198.18.8.77:80/')==502
 assert send({'Proxy-Authorization':'Basic '+token},'http://192.0.0.88:80/')==502
 assert call('status')['state']['synthetic_seen']>=1
 assert call('status')['state']['synthetic_allowed']==0
 assert call('setLimit',value='8',unit='Mbps')['state']['cap_bytes_per_second']==1_000_000
 assert call('setEnabled',enabled=False)['state']['limiting'] is False
 assert call('setLimit',value='5',unit='MB/s')['state']['limiting'] is False
 assert call('setEnabled',enabled=True)['state']['limiting'] is True
 assert not call('setEnabled')['ok']
 assert call('status')['state']['port']==port
 assert call('stop')['ok']; assert p.wait(timeout=5)==0
 try:socket.create_connection(('127.0.0.1',port),timeout=1);raise AssertionError('port still open')
 except OSError:pass
 print('PASS: hello, validation, random port, authentication, private destination blocking, no status endpoint, live cap, stop cleanup')
finally:
 if p.poll() is None:p.kill();p.wait()

p=subprocess.Popen([sys.argv[1]],stdin=subprocess.PIPE,stdout=subprocess.PIPE)
try:
 r=call('start',value='1',unit='MB/s',vpnCompat=True);assert r['ok'] and r['state']['vpn_compat'] is True
 port=r['state']['port'];token=base64.b64encode(('csl:'+r['auth']['password']).encode()).decode()
 assert send({'Proxy-Authorization':'Basic '+token},'http://198.18.8.77:80/')==502
 assert send({'Proxy-Authorization':'Basic '+token},'http://192.0.0.88:80/')==502
 assert call('status')['state']['synthetic_seen']>=1 and call('status')['state']['synthetic_allowed']==0
 p.stdin.close();assert p.wait(timeout=2)==0
 try:socket.create_connection(('127.0.0.1',port),timeout=1);raise AssertionError('port still open after native EOF')
 except OSError:pass
 print('PASS: opt-in compatibility mode, literal synthetic IP remains blocked, native EOF cleanup')
finally:
 if p.poll() is None:p.kill();p.wait()
