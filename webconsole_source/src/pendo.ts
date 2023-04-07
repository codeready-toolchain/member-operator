// initialize pendo
(function (apiKey) {
    (function (p, e, n, d, o) {
        var v, w, x, y, z;
        o = p[d] = p[d] || {};
        o._q = o._q || [];
        v = ['initialize', 'identify', 'updateOptions', 'pageLoad', 'track'];
        for (w = 0, x = v.length; w < x; ++w)
            (function (m) {
                o[m] =
                    o[m] ||
                    function () {
                        o._q[m === v[0] ? 'unshift' : 'push'](
                            [m].concat([].slice.call(arguments, 0)),
                        );
                    };
            })(v[w]);
        y = e.createElement(n);
        y.async = !0;
        y.src = 'https://{PENDO_HOST}/agent/static/' + apiKey + '/pendo.js';
        z = e.getElementsByTagName(n)[0];
        z.parentNode.insertBefore(y, z);
    })(window, document, 'script', 'pendo');
})('{PENDO_KEY}');

const PREFIX = 'toolchain.dev.openshift.com/';

const SSO_USER_ID = `${PREFIX}sso-user-id`;
const SSO_ACCOUNT_ID = `${PREFIX}sso-account-id`;
const SSO_EMAIL :string = `${PREFIX}user-email`;

let initialized = false;

export default (eventType: string, properties?: any) => {
    if (eventType === 'identify') {
        const { user } = properties;
        if (user) {
            const ssoUserId = user.metadata.annotations?.[SSO_USER_ID];
            const ssoAccountId = user.metadata.annotations?.[SSO_ACCOUNT_ID];
            const email = user.metadata.annotations?.[SSO_EMAIL];
            const domain = email.match(/@(.+)/)[1];
            if (ssoUserId && (window as any).pendo) {
                (window as any).pendo[initialized ? 'identify' : 'initialize']({
                    visitor: {
                        id: ssoUserId,
                        email_domain: domain,
                    },
                    ...(ssoAccountId
                        ? {
                            account: {
                                id: ssoAccountId,
                            },
                        }
                        : null),
                });
                initialized = true;
            }
        }
    }
};
