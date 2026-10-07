"""Independent synthetic fixture oracle. Run explicitly, never from normal tests.
Uses rational arithmetic for F1, utility, half-even fitness and coverage.
No real corpus, run or score is represented here.
"""
import copy
import hashlib
import json
from fractions import Fraction
from pathlib import Path

ROOT = Path(__file__).parent

def encode(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()

def digest(value):
    return 'sha256:' + hashlib.sha256(encode(value)).hexdigest()

def write(name, value):
    (ROOT / name).write_bytes(encode(value))

registry = json.loads(Path('pkg/evidence/testdata/registry.json').read_bytes())
registry['models'][0]['efforts'] = ['high', 'max', 'none']
write('registry.json', registry)
mapping = {'schema_version':'evalrun-mapping-v1','name':'synthetic-map','version':'1','models':[{'external_name':'Synthetic Model','internal_id':'gpt-6.1-sol'}],'runtimes':[{'external_name':'Synthetic Runtime','internal_id':'codex'}]}
write('mapping.json', mapping)
protocol = {'schema_version':'synthetic-review-protocol-v1','synthetic':True,'completion_rule':'full independently adjudicated case set','f1':'2*TP/(2*TP+FP+FN)','ground_truth':'planted and independently adjudicated; human ambiguity resolution','case_weighting':'unweighted','task_class':'synthetic Go review','diagnostics_separate':True}
write('protocol.json', protocol)
benchmark = {'id':'internal-review-set','version':'1','publisher':'synthetic-fixture','dataset':'synthetic-revision-1','split':'holdout','protocol_digest':digest(protocol),'categories':[{'category':'review.code','note':'synthetic Go review only'}],'metrics':[{'name':'false_findings','unit':'count','direction':'lower_better','description':'diagnostic false findings'},{'name':'missed_defects','unit':'count','direction':'lower_better','description':'diagnostic missed defects'},{'name':'review_f1','unit':'ratio','direction':'higher_better','scale':{'min':0,'max':1},'description':'unweighted F1; only quality input'},{'name':'true_defects_found','unit':'count','direction':'higher_better','description':'diagnostic true defects'}],'grading_method':'human'}
candidate = json.loads(Path('pkg/evidence/testdata/candidate.json').read_bytes())['candidates'][0]
subject = copy.deepcopy(candidate)
subject['model_name']='Synthetic Model';del subject['model_id']
subject['runtime_name']='Synthetic Runtime';del subject['runtime']
run = {'schema_version':'evalrun-v1','run_id':'synthetic-run','exported_at':'2026-10-02T00:00:00Z','mapping_ref':{'name':mapping['name'],'version':mapping['version']},'mapping_digest':digest(mapping),'benchmark':benchmark,'results':[]}
f1 = Fraction(2*8, 2*8+2+2)
assert f1 == Fraction(4,5)
for metric,value in [('false_findings',2),('missed_defects',2),('review_f1',float(f1)),('true_defects_found',8)]:
    run['results'].append({'result_id':'synthetic-result','subject':copy.deepcopy(subject),'categories':['review.code'],'facets':{'languages':['go'],'platforms':['macos']},'metric':metric,'value':value,'sample_count':10,'grading_method':'human','observed_at':'2026-10-01T12:00:00Z','raw_artifact_ref':'artifact:synthetic-only','cost':{'tokens_in':0,'tokens_out':12,'usd':0,'wall_s':1}})
write('export.json',run)
(ROOT/'export-reformatted.json').write_text(json.dumps(run,indent=2))
v1 = {'schema_version':'evidence-v1','name':'role-suitability','version':'1','normalisations':[{'benchmark_ref':{'id':'bug-hunt-bench','version':'2026-09-13'},'metric':'fixed','min':0,'max':105,'direction':'higher_better','categories':['code.fix']}],'confidence_weights':[{'key':'high','value':1},{'key':'low','value':0.25},{'key':'medium','value':0.6}],'basis_weights':[{'key':'benchmark-reading','value':0.4},{'key':'incident','value':0.8},{'key':'operator-judgement','value':0.6},{'key':'review-outcomes','value':1},{'key':'run-outcomes','value':0.8}],'partial_discount':0.5,'strong_threshold':0.4,'adequate_threshold':0.2,'stale_note_weight':0}
v2 = copy.deepcopy(v1);v2['version']='2';v2['normalisations'].append({'benchmark_ref':{'id':'internal-review-set','version':'1'},'metric':'review_f1','min':0,'max':1,'direction':'higher_better','categories':['review.code']})
write('review-derivation-v2.json',v2)
(ROOT/'review-derivation-v2.digest').write_text(digest(v2)+'\n')
(ROOT/'review-derivation-v1.digest').write_text(digest(v1)+'\n')
observations=[]
for row in run['results']:
    obs={k:copy.deepcopy(row[k]) for k in ['categories','facets','metric','value','sample_count','grading_method','observed_at','cost']}
    obs.update({'benchmark_ref':{'id':benchmark['id'],'version':benchmark['version']},'subject':copy.deepcopy(candidate),'subject_resolution':'resolved','evidence_kind':'measured','provenance':{'source':'internal-evals','retrieved_at':run['exported_at'],'raw_artifact_ref':row['raw_artifact_ref'],'origin_ref':'evalrun:synthetic-run:synthetic-result'}})
    obs['id']='obs:'+digest(obs)[7:];observations.append(obs)
observations.sort(key=lambda x:x['id'])
doc={'schema_version':'evidence-v1','kind':'evidence-import','source':'internal-evals','importer':{'name':'evalrun','version':'1+map.'+digest(mapping)[7:]},'imported_at':run['exported_at'],'measured_by':'internal','benchmarks':[benchmark],'observations':observations,'notes':[],'retractions':[]}
write('import.canonical.json',doc)
(ROOT/'import.digest').write_text(digest(doc)+'\n')
snap={'schema_version':'evidence-v1','kind':'evidence-snapshot','imports':[digest(doc)],'taxonomy_version':'v1','registry_ref':registry['reference'],'registry_digest':digest(registry)}
write('snapshot.canonical.json',snap)
(ROOT/'snapshot.digest').write_text(digest(snap)+'\n')
counts=[{'name':'CompleteSyntheticCounts','counts':{'true_defects_found':8,'false_findings':2,'missed_defects':2,'expected_cases':10,'evaluated_cases':10,'correct_clean_reviews':0},'expected':{'complete':True,'review_f1':float(f1),'sample_count':10,'correct_clean_reviews':0}},{'name':'ZeroF1Denominator','counts':{'true_defects_found':0,'false_findings':0,'missed_defects':0,'expected_cases':5,'evaluated_cases':5,'correct_clean_reviews':5},'expected':{'complete':True,'sample_count':5,'correct_clean_reviews':5}},{'name':'IncompleteCaseSet','counts':{'true_defects_found':8,'false_findings':2,'missed_defects':2,'expected_cases':10,'evaluated_cases':9,'correct_clean_reviews':0},'expected':{'complete':False,'sample_count':9,'correct_clean_reviews':0}}]
write('counts.json',{'schema_version':'synthetic-counts-v1','vectors':counts})
vectors=[]
for name,partial,missing,version in [('CompleteSyntheticDirect',False,False,'2'),('CompleteSyntheticPartial',True,False,'2'),('MissingSpecDirect',False,True,'2'),('MissingSpecPartial',True,True,'2'),('ReviewUnderDerivation1',False,False,'1')]:
    cand=copy.deepcopy(candidate)
    if partial: del cand['engine_profile']
    req={'schema_version':'evidence-v1','project':'synthetic-project','role':'reviewer','requirements':[{'category':'review.code','weight':7500 if missing else 10000}], 'mapping':{'schema_version':'evidence-v1','name':'synthetic-requirements','version':'1','roles':[],'workloads':[]}}
    if missing: req['requirements'].append({'category':'review.spec','weight':2500})
    utility=f1*(Fraction(1,2) if partial else 1)
    expected={'fitness_bp':round(5000*(1+utility)),'coverage_bp':round((7500 if missing else 10000)*(Fraction(1,2) if partial else 1)),'utility':float(utility)} if version=='2' else {'coverage_bp':0}
    vectors.append({'name':name,'candidate':cand,'requirements':req,'derivation_version':version,'expected':expected})
    assert version=='1' or expected['fitness_bp']==(7000 if partial else 9000)
write('derivation-vectors.json',{'schema_version':'synthetic-derivation-vectors-v1','vectors':vectors})
# Full views are independently assembled from the protocol and exact oracle.
for vector in vectors:
    d=v1 if vector['derivation_version']=='1' else v2
    cand=vector['candidate'];req=vector['requirements'];e=vector['expected']
    inputs={'schema_version':'evidence-v1','evidence_snapshot_digest':digest(snap),'derivation_digest':digest(d),'role_requirements_digest':digest(req),'weights_policy_digest':digest({'schema_version':'evidence-v1','confidence':d['confidence_weights'],'basis':d['basis_weights'],'partial':d['partial_discount'],'strong':d['strong_threshold'],'adequate':d['adequate_threshold'],'stale':d['stale_note_weight']}),'evaluated_at':'2026-10-02T00:00:00Z','candidate_set_digest':digest({'schema_version':'evidence-v1','candidates':[cand]})}
    issues=[{'code':'observation_unmapped','ref':o['id'],'detail':'internal-review-set@1/'+o['metric']+'/review.code'} for o in observations if d['version']=='1' or o['metric']!='review_f1'];issues.sort(key=lambda x:(x['code'],x['ref'],x['detail']))
    result={'role':'reviewer','candidate':cand,'grade':'strong' if 'utility' in e else 'unknown','scale':{'min':-1,'max':1},'coverage':[{'category':r['category'],'kind':'measured' if r['category']=='review.code' and 'utility' in e else 'none','match':('partial' if 'engine_profile' not in cand else 'direct') if r['category']=='review.code' and 'utility' in e else 'none'} for r in req['requirements']],'contributions':[],'derivation':{'name':'role-suitability','version':d['version'],'digest':digest(d)}}
    if 'utility' in e:
        result['score']=e['utility'];result['contributions']=[{'ref':next(o['id'] for o in observations if o['metric']=='review_f1'),'category':'review.code','direction':'+','weight':e['utility'],'stale':False}]
    view={'schema_version':'evidence-v1','id':digest(inputs),'inputs':inputs,'requirements':req,'derivation':d,'candidates':[cand],'results':[result],'issues':issues}
    write(vector['name']+'.view.json',view);(ROOT/(vector['name']+'.view.digest')).write_text(digest(view)+'\n')
# Refusals are full frozen input fixtures, not generated by normal tests.
refusals=[]
def bad(name,mutate,code):
    v=copy.deepcopy(run);m=copy.deepcopy(mapping);mutate(v,m);refusals.append({'name':name,'export':v,'mapping':m,'code':code})
bad('ChangedMappingName',lambda v,m:m.update(name='other'),'evidence_evalrun_mapping_mismatch')
bad('ChangedMappingVersion',lambda v,m:m.update(version='2'),'evidence_evalrun_mapping_mismatch')
bad('ChangedMappingDigest',lambda v,m:m['models'][0].update(internal_id='unregistered'),'evidence_evalrun_mapping_mismatch')
bad('DuplicateResultMetric',lambda v,m:v['results'].append(copy.deepcopy(v['results'][0])),'evidence_evalrun_result_conflict')
bad('ConflictingSharedSubject',lambda v,m:v['results'][1]['subject'].update(effort='max'),'evidence_evalrun_result_conflict')
for fact in ['raw_artifact_ref','observed_at','sample_count']:
    bad('ConflictingShared'+fact,lambda v,m,f=fact:v['results'][1].update({f:11 if f=='sample_count' else ('2026-10-01T11:00:00Z' if f=='observed_at' else 'artifact:other')}),'evidence_evalrun_result_conflict')
bad('MissingProtocol',lambda v,m:v['benchmark'].pop('protocol_digest'),'evidence_evalrun_protocol_required')
bad('UnknownProtocol',lambda v,m:v['benchmark'].update(protocol_digest='unknown'),'evidence_evalrun_protocol_required')
bad('ZeroSampleCount',lambda v,m:[x.update(sample_count=0) for x in v['results']],'evidence_evalrun_invalid_result')
bad('NegativeCost',lambda v,m:v['results'][0]['cost'].update(usd=-1),'evidence_invalid_value')
bad('InvalidRunID',lambda v,m:v.update(run_id='bad:id'),'evidence_evalrun_invalid_result')
bad('InvalidResultID',lambda v,m:v['results'][0].update(result_id='bad:id'),'evidence_evalrun_invalid_result')
bad('UnsupportedExportSchema',lambda v,m:v.update(schema_version='evalrun-v2'),'evidence_evalrun_schema')
bad('UnsupportedMappingSchema',lambda v,m:m.update(schema_version='evalrun-mapping-v2'),'evidence_evalrun_schema')
bad('UnorderedResults',lambda v,m:v['results'].reverse(),'canonical_unordered')
bad('InvalidMetric',lambda v,m:v['results'][0].update(metric='absent'),'evidence_invalid_metric')
bad('InvalidCategory',lambda v,m:v['results'][0].update(categories=['review.spec']),'evidence_invalid_category')
bad('InvalidExportTime',lambda v,m:v.update(exported_at='unknown'),'evidence_invalid_time')
bad('InvalidObservationTime',lambda v,m:v['results'][0].update(observed_at='unknown'),'evidence_invalid_time')
bad('SupersedesNote',lambda v,m:v['results'][0].update(supersedes=['note:'+'0'*64]),'evidence_invalid_target')
bad('SupersedesMalformed',lambda v,m:v['results'][0].update(supersedes=['obs:bad']),'evidence_invalid_target')
bad('MissingReviewDataset',lambda v,m:v['benchmark'].pop('dataset'),'evidence_evalrun_invalid_result')
bad('MissingReviewSplit',lambda v,m:v['benchmark'].pop('split'),'evidence_evalrun_invalid_result')
bad('DuplicateMappingName',lambda v,m:m['models'].append(copy.deepcopy(m['models'][0])),'canonical_duplicate_key')
bad('UnorderedMapping',lambda v,m:m['models'].insert(0,{'external_name':'Z Synthetic','internal_id':'gpt-6.1-sol'}),'canonical_unordered')
write('refusals.json',{'schema_version':'synthetic-refusals-v1','vectors':refusals})
raw=encode(run).decode()
for name,text in [('null.json',raw.replace('"sample_count":10','"sample_count":null',1)),('nonfinite.json',raw.replace('"value":2','"value":1e999',1)),('unknown-field.json',raw.replace('"run_id":','"unexpected":1,"run_id":',1)),('duplicate-key.json',raw.replace('"run_id":','"run_id":"other","run_id":',1)),('trailing.json',raw+' {}')]:
    (ROOT/name).write_text(text)
