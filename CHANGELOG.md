# Changelog

## 0.1.0-alpha.0 (2026-11-28)


### ⚠ BREAKING CHANGES

* **cli:** --offline and network.mode enforced at every egress
* **cli:** learn honours the lock; restore, --frozen, list --check
* **install:** lock pin in ExpectedDigest, admission.require_pin
* **cli:** learn name@version and url#ref pins
* **registry:** keep every name+version; serve digest and all versions
* **cli:** folder scopes via --dir, list, effective warmup, train -r
* **cli:** honour allowlisted project cli.yaml keys only
* **install:** refuse untrusted state records and escaping install paths
* **config:** honour allowlisted project config keys only
* **install:** ignore author-supplied guard allowances
* **install:** add skill manifest admission stage
* **cli:** use kit -c/--config; drop gym --config
* **config:** load layered config via kit core/config

### Features

* add warmup command for progressive disclosure ([e94ac88](https://github.com/hop-top/gym/commit/e94ac88c76a9b8ab0c5bd548d30f945db637e122))
* **cheat:** add toolspec cheat sheets and tip adapter ([92318dd](https://github.com/hop-top/gym/commit/92318ddd83f594c5407a236617b546a293bafa83))
* **cli:** --offline and network.mode enforced at every egress ([2f2d02c](https://github.com/hop-top/gym/commit/2f2d02ccfa9244799e631b890c4e878da57375e5))
* **cli:** archive sources, archive digest, lock rule for any out-of-tree local source ([269c9d3](https://github.com/hop-top/gym/commit/269c9d37cf9b2b2af386d1778d6883dc48ed86d9))
* **cli:** coach hire accepts a local skills.json path ([12edcbf](https://github.com/hop-top/gym/commit/12edcbf3ddb054ebe147da606af049bc4207b1d1))
* **cli:** folder scopes via --dir, list, effective warmup, train -r ([b513930](https://github.com/hop-top/gym/commit/b51393000edf381a5735885da14aa950714f276f))
* **cli:** learn and train write the lock ([8112725](https://github.com/hop-top/gym/commit/811272550130a4aa8d06272b69ed8ce20a59da65))
* **cli:** learn honours the lock; restore, --frozen, list --check ([f703561](https://github.com/hop-top/gym/commit/f703561ebe142deb7040d9c75d21ff7ff033c08c))
* **cli:** learn name@version and url#ref pins ([bb75a35](https://github.com/hop-top/gym/commit/bb75a35f9aa03e5d960fcfde33fd9af673df008c))
* **cli:** unlearn --dir ([b3c3805](https://github.com/hop-top/gym/commit/b3c38052080f06eb86cd4b645a42139ff61b3ffd))
* **cli:** unlearn command ([3bf2e69](https://github.com/hop-top/gym/commit/3bf2e69297e3570dda9fd9364b39238b968ef301))
* **cli:** unlearn removes the lock entry ([e221889](https://github.com/hop-top/gym/commit/e2218898b56ce04021435cd31e0921d91e828aea))
* **cli:** use kit -c/--config; drop gym --config ([968d148](https://github.com/hop-top/gym/commit/968d148890a21dcd93af1a4107aee1ec643daf0e))
* **cli:** wire bus, guard, cheat, progress and coach-hire wizard ([0e8922c](https://github.com/hop-top/gym/commit/0e8922c33b87583150cb2824975d6a2a809973d0))
* **coach:** judge coaches via rsx ([67546af](https://github.com/hop-top/gym/commit/67546afd8a414a9151476061b200d860a8c806c6))
* **config:** chain-merge folder config across scope roots ([2bde7f3](https://github.com/hop-top/gym/commit/2bde7f38b55191cfb914a7ab15cccf31bbc9c684))
* **eventbus:** add bus adapter with request/response gates ([97a59fd](https://github.com/hop-top/gym/commit/97a59fd991a179ecc4791c3abb81354ca978f6c6))
* **events:** add event topics and payload types ([324386d](https://github.com/hop-top/gym/commit/324386d0589935afbdbb5500b0994dc63d996949))
* **events:** folder on install and upgrade payloads ([1d0e7f9](https://github.com/hop-top/gym/commit/1d0e7f96e3e0f0ce6a64e3992e6467a862e07616))
* **events:** skill uninstall topics and payloads ([6f03cc1](https://github.com/hop-top/gym/commit/6f03cc132849dadfe68f2cee9561562ab1d17a2d))
* **events:** source_host on install-requested and upgrade-planned ([e168fc3](https://github.com/hop-top/gym/commit/e168fc36d287b8f260c3de79f6aa088d819c54fc))
* **fsutil:** atomic file write; state files use it ([4f6e92b](https://github.com/hop-top/gym/commit/4f6e92b93f331d42a22433d6c9384599e9aa605a))
* **guard:** add CEL policy guard for installs and upgrades ([eb0808c](https://github.com/hop-top/gym/commit/eb0808c7272f59531806e501a9def33f830a7e53))
* **guard:** add-only policies across folder chain ([9f12fb4](https://github.com/hop-top/gym/commit/9f12fb4ff15a2a17effcc19c2b74011fcf10f0ba))
* **gym:** add coach commands ([04dbcd0](https://github.com/hop-top/gym/commit/04dbcd067618d8e29b917ad08efd6f50d43fe691))
* initial gym skill package manager ([0aa4fb0](https://github.com/hop-top/gym/commit/0aa4fb06728e6419ace3fae2603763ed7c18e6ad))
* **install:** add skill manifest admission stage ([dceb5b1](https://github.com/hop-top/gym/commit/dceb5b1bb12417950d07f3ab5fd3871747920342))
* **install:** archive sources, hardened git, network policy on git ([d5c2998](https://github.com/hop-top/gym/commit/d5c2998e9e7e4cb1844be12fd616aa5ebb073d9b))
* **install:** lock pin in ExpectedDigest, admission.require_pin ([2199e70](https://github.com/hop-top/gym/commit/2199e7099c93885dd32e908dcb41ef164b5d3609))
* **install:** trusted skill removal ([5383486](https://github.com/hop-top/gym/commit/538348611cf45b247939744d1bdaabc1eff129df))
* **lock:** gym.lock/v1 read, write, validate ([52a7c2a](https://github.com/hop-top/gym/commit/52a7c2ab8362b683feef371ef192081209c0544b))
* **netpolicy:** host allow-list policy, network config keys, tighten-only fold ([c286f4d](https://github.com/hop-top/gym/commit/c286f4d796ae6a4b02ffaee2e750c3750929b033))
* **registry:** keep every name+version; serve digest and all versions ([68f7d8a](https://github.com/hop-top/gym/commit/68f7d8a1d5bcd1144258b925d7e0cddd23793721))
* **registry:** policy-checked fetcher with bearer and git-credential auth ([7815ba9](https://github.com/hop-top/gym/commit/7815ba9627c7a483540175e2ab4c575cdf1206da))
* **scope:** chain of folder roots ([84bfcf0](https://github.com/hop-top/gym/commit/84bfcf0438887109e88ca5417c8d69fab7a42c2e))
* support multi-path installs and agent filtering ([5ee20fa](https://github.com/hop-top/gym/commit/5ee20fa70bb47e155592bf49123da2a74dc63abe))


### Bug Fixes

* **build:** use tabs in Makefile recipes ([82e0479](https://github.com/hop-top/gym/commit/82e0479065215d7bc598e34e93a317fb724ae81d))
* **cli:** bind every gym persistent flag to the gate ([344838c](https://github.com/hop-top/gym/commit/344838ce0742dcc897fe239047915ab61275be79))
* **cli:** find committed lock in list --check on fresh clone ([df2eead](https://github.com/hop-top/gym/commit/df2eead5edf980ab4556d3e8c2fe20c61111de85))
* **cli:** gate train on each scope's own policy chain ([4a7d5f2](https://github.com/hop-top/gym/commit/4a7d5f2b1d1b48bd8c8ac0db4068dafda1399964))
* **cli:** honour allowlisted project cli.yaml keys only ([9e89e59](https://github.com/hop-top/gym/commit/9e89e590e328098347c04c0ac3c57301efe3f556))
* **cli:** publish install.rejected for validator and policy denies ([5262bc6](https://github.com/hop-top/gym/commit/5262bc6ef10021e2ad7626746da26f91cf9cf3f4))
* **cli:** skip untrusted state records in warmup and cheat ([b2b4105](https://github.com/hop-top/gym/commit/b2b4105435e674ec1a302ce9ad46ee665839be2f))
* **cli:** surface guard startup failure; deny strict gate naming cause ([12ff4fd](https://github.com/hop-top/gym/commit/12ff4fd992df702b3f8a13206438d2179d90f8f3))
* **config:** honour allowlisted project config keys only ([3c3d5fc](https://github.com/hop-top/gym/commit/3c3d5fc3a1266e3648b72195fa854cbe182f2519))
* **config:** read config.json, write config.yaml ([5bba26a](https://github.com/hop-top/gym/commit/5bba26aeda5208b1a12adef62acb6e2f38de5ba7))
* dedupe skills by name+path instead of name only ([4b14f4b](https://github.com/hop-top/gym/commit/4b14f4b4361dff61f6a04dfa7a60f0730ab4705f))
* **guard:** gate a scope with unbuildable engine by the start-chain engine ([48f2a13](https://github.com/hop-top/gym/commit/48f2a13b73d0ce8445ad2280a718048e4466674a))
* **guard:** project policies add-only ([e7e9897](https://github.com/hop-top/gym/commit/e7e98974ccc8cfab115eb1072283a8333b341666))
* **guard:** scan script files and text files in staged skills ([a571416](https://github.com/hop-top/gym/commit/a57141661557d714f6530982be12696564ecfa5e))
* **guard:** select policy engine per request policy root ([7d2616d](https://github.com/hop-top/gym/commit/7d2616db25b72849487e9bbde87a96168b26c815))
* **guard:** skip broken project policy files instead of failing the load ([c759755](https://github.com/hop-top/gym/commit/c7597552d5b6b853ee3945cc774e932f01d14505))
* **install:** default validator runs skills-ref validate ([fad6151](https://github.com/hop-top/gym/commit/fad615176dd613d1504b9d8c44bda916c16b7cc4))
* **install:** ignore author-supplied guard allowances ([7b07427](https://github.com/hop-top/gym/commit/7b07427882a6d9c9d8b127fadbf088607c8397ed))
* **install:** leave .git out of installed copy ([c355871](https://github.com/hop-top/gym/commit/c355871cdc304997783579168ed912608d4167d8))
* **install:** refuse untrusted state records and escaping install paths ([05a38e1](https://github.com/hop-top/gym/commit/05a38e175d1197747323b213b0cfdff8028b5551))
* **install:** reject and redact credentials in source URLs ([819a269](https://github.com/hop-top/gym/commit/819a2699b1cc52d1e3f219f8c4d528dd77b3537c))
* **install:** skip modified check for dir-only trees ([347eae3](https://github.com/hop-top/gym/commit/347eae3fbd0f56d710ed45f0573bac7ed0f1faf1))
* **install:** type validator failures ([607688b](https://github.com/hop-top/gym/commit/607688bbe3422192105350bae2102bea2d9fa67d))
* **lock:** never write a dir entry a project lock would refuse to read ([516f3e8](https://github.com/hop-top/gym/commit/516f3e8198628aecafdf3900021e59fac1556d1a))
* **xdg:** decide previous-layout install once for all kinds ([205ba6f](https://github.com/hop-top/gym/commit/205ba6fa7e14b104e2126e3b00a7488a6b9eebda))
* **xdg:** explicit XDG_* always wins over previous dir ([76a4042](https://github.com/hop-top/gym/commit/76a404216075eb1ec4b22bdd7ee04713402cfcba))
* **xdg:** keep previous XDG-style dirs for existing installs ([ec461fa](https://github.com/hop-top/gym/commit/ec461fa35fb8e417107caf7bc21fb53da9bf878e))


### Code Refactoring

* **config:** load layered config via kit core/config ([8d17998](https://github.com/hop-top/gym/commit/8d179987bbd27ee8800285e871db6bbb781a9ec9))
