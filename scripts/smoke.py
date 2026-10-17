# Integration test: uses existing encrypted CLI logins in temporary roots.
# Requires make build server and consumes a few real model requests per account.
import inspect,sys,socket,tempfile,pathlib,shutil,subprocess,urllib.request,urllib.error,json,time,os,signal

def run(root):
    base=pathlib.Path(__file__).resolve().parents[1]
    root.chmod(0o700)
    for variant,folder in [('global','.qoder'),('cn','.qoder-cn')]:
     dest=root/variant/'.auth';dest.mkdir(parents=True,mode=0o700)
     for f in ['user','machine_id']:shutil.copyfile(pathlib.Path.home()/folder/'.auth'/f,dest/f);(dest/f).chmod(0o600)
    shutil.copytree(base/'auths',root/'auths')
    for variant,cli in [('global','qodercli'),('cn','qoderclicn')]:
     executable=shutil.which(cli)
     assert executable, f'{cli} not installed'
     wrapper=root/f'{variant}-cli'
     pidfile=root/f'{variant}.pid'
     wrapper.write_text(f'#!{sys.executable}\nimport os,sys\nif "--list-models" not in sys.argv: open({str(pidfile)!r}, "w").write(str(os.getpid()))\nos.execv({executable!r},[{executable!r}]+sys.argv[1:])\n')
     wrapper.chmod(0o700)
    sock=socket.socket();sock.bind(('127.0.0.1',0));port=sock.getsockname()[1];sock.close()
    config=f'''config-version: 8
    server:
      host: 127.0.0.1
      port: {port}
    access:
      api-keys: [smoke-key]
    management:
      disable-control-panel: true
    oauth:
      auth-dir: {root}/auths
    routing:
      retry:
        request-retry: 0
    plugins:
      enabled: true
      dir: {base}/build/plugins
      configs:
        cpa-qoder:
          enabled: true
          helper: {base}/build/cpa-qoder-helper
          global:
            cli: {root}/global-cli
            config-dir: {root}/global
          cn:
            cli: {root}/cn-cli
            config-dir: {root}/cn
    '''
    (root/'config.yaml').write_text(inspect.cleandoc(config))
    log=(root/'server.log').open('w+')
    p=subprocess.Popen([str(base/'build/cli-proxy-api'),'--config',str(root/'config.yaml'),'--local-model'],cwd=root,stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
    url=f'http://127.0.0.1:{port}'
    def request(path,body=None):
     data=None if body is None else json.dumps(body).encode()
     req=urllib.request.Request(url+path,data=data,headers={'Authorization':'Bearer smoke-key','Content-Type':'application/json'})
     return urllib.request.urlopen(req,timeout=65)
    try:
     models=[]
     time.sleep(4)
     for _ in range(120):
      if p.poll() is not None:raise RuntimeError('CPA exited')
      try:
       models=json.load(request('/v1/models'))['data']
       if any(m['id'].startswith('qoder/') for m in models) and any(m['id'].startswith('qoder-cn/') for m in models):break
      except Exception:pass
      time.sleep(.25)
     print('MODEL_IDS',json.dumps([m['id'] for m in models]),flush=True)
     assert any(m['id']=='qoder-cn/Qwen3.8-Flash' for m in models)
     for model in ['qoder/Qwen3.8-Flash','qoder-cn/Qwen3.8-Flash']:
      body={'model':model,'messages':[{'role':'user','content':'Reply with exactly OK.'}],'reasoning_effort':'none','max_tokens':32}
      out=json.load(request('/v1/chat/completions',body));print('COMPLETE',model,json.dumps(out),flush=True);assert out['choices'][0]['message']['content'].strip()=='OK'
      body['stream']=True
      stream=request('/v1/chat/completions',body).read().decode();chunks=[json.loads(line[6:]) for line in stream.splitlines() if line.startswith('data: ') and '[DONE]' not in line]
      text=''.join(c['choices'][0]['delta'].get('content','') for c in chunks if c.get('choices'))
      print('STREAM',model,'chunks',len(chunks),'text',repr(text),'done','[DONE]' in stream,flush=True);assert text.strip()=='OK' and '[DONE]' in stream
      body={'model':model,'messages':[{'role':'user','content':'Use weather to get the weather in Beijing.'}],'reasoning_effort':'none','tools':[{'type':'function','function':{'name':'weather','description':'Get weather for a city','parameters':{'type':'object','properties':{'city':{'type':'string'}},'required':['city']}}}]}
      out=json.load(request('/v1/chat/completions',body));choice=out['choices'][0];print('TOOL',model,json.dumps(choice),flush=True);assert choice['finish_reason']=='tool_calls' and choice['message']['tool_calls'][0]['function']['name']=='weather'
      for name,parameters,prompt in [
       ('weather',{'type':'object','properties':{'city':{'type':'string'}},'required':['city']},'Use weather to get the weather in Beijing.'),
       ('ping',{'type':'object','properties':{}},'Call ping now.'),
      ]:
       streamed={'model':model,'stream':True,'messages':[{'role':'user','content':prompt}],'reasoning_effort':'none','tools':[{'type':'function','function':{'name':name,'description':'Call this function to answer the request','parameters':parameters}}]}
       wire=request('/v1/chat/completions',streamed).read().decode()
       chunks=[json.loads(line[6:]) for line in wire.splitlines() if line.startswith('data: ') and line[6:]!='[DONE]']
       calls={}
       for chunk in chunks:
        for delta in chunk['choices'][0]['delta'].get('tool_calls',[]):
         call=calls.setdefault(delta['index'],{'name':'','arguments':''})
         call['name']+=delta.get('function',{}).get('name','')
         call['arguments']+=delta.get('function',{}).get('arguments','')
       assert calls and all(c['name']==name and isinstance(json.loads(c['arguments']),dict) for c in calls.values()),calls
       assert chunks[-1]['choices'][0]['finish_reason']=='tool_calls' and wire.count('data: [DONE]')==1
       print('STREAM_TOOL',model,json.dumps(calls),flush=True)
      messages=body['messages']+[choice['message'],{'role':'tool','tool_call_id':choice['message']['tool_calls'][0]['id'],'content':'Sunny, 20 degrees.'},{'role':'user','content':'Repeat the tool result exactly, no extra words.'}]
      follow={'model':model,'messages':messages,'reasoning_effort':'none','max_tokens':64}
      out=json.load(request('/v1/chat/completions',follow));print('FOLLOWUP',model,json.dumps(out['choices'][0]),flush=True);assert 'Sunny' in out['choices'][0]['message']['content']
     # Disconnect after the actual CLI has started and verify its process is reaped.
      body={'model':model,'messages':[{'role':'user','content':'Write a long detailed story about the sea.'}],'stream':True,'max_tokens':4096}
      pidfile=root/('cn.pid' if model.startswith('qoder-cn/') else 'global.pid')
      pidfile.unlink(missing_ok=True)
      response=request('/v1/chat/completions',body)
      response.readline()
      deadline=time.monotonic()+5
      while not pidfile.exists() and time.monotonic()<deadline:time.sleep(.02)
      assert pidfile.exists(), 'No real CLI observed for disconnect test'
      cli_pid=int(pidfile.read_text())
      response.close()
      def alive():
       try:os.kill(cli_pid,0);return True
       except ProcessLookupError:return False
      deadline=time.monotonic()+6
      while alive() and time.monotonic()<deadline:time.sleep(.05)
      assert not alive(), 'CLI leaked after HTTP disconnect'
      print('DISCONNECT',model,'child reaped',flush=True)
     print('E2E PASSED',flush=True)
    except Exception as e:
     print('FAILED',type(e).__name__,str(e),flush=True)
     if isinstance(e,urllib.error.HTTPError): print('HTTP_BODY',e.read().decode(),flush=True)
     log.flush();log.seek(0);print('SERVER_LOG',log.read()[-12000:],flush=True)
     raise
    finally:
     p.terminate()
     try:p.wait(timeout=10)
     except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait()
     log.close()


def main():
    with tempfile.TemporaryDirectory(prefix='cpa-qoder-e2e-') as directory:
        run(pathlib.Path(directory))


if __name__ == '__main__':
    main()
