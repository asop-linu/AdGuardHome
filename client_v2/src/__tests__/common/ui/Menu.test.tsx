import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@solidjs/testing-library';
import userEvent from '@testing-library/user-event';

vi.mock('@solidjs/router', () => ({
    useLocation: (): { pathname: string } => ({ pathname: '/' }),
}));

vi.mock('panel/common/intl', () => {
    const intl = {
        getMessage: (key: string): string => {
            const messages: Record<string, string> = {
                dashboard: 'Dashboard',
                settings: 'Settings',
                query_log: 'Query log',
                setup_guide: 'Setup guide',
                logout: 'Log out',
                clients: 'Clients',
                protocols: 'Settings: Protocols',
                blocklists_title: 'Blocklists (beta)',
                allowlists: 'Allowlists (beta)',
                dns_rewrites: 'DNS rewrites',
                blocked_services: 'Blocked services',
                user_rules_title: 'Custom filtering rules',
                settings_general_short: 'General settings',
            };
            return messages[key] || key;
        },
    };
    return { default: intl };
});

vi.mock('panel/lib/theme', () => ({
    default: {
        common: { textOverflow: 'textOverflow' },
        link: { link: 'link' },
    },
}));

vi.mock('panel/common/ui/Icon', () => ({
    Icon: (): null => null,
}));

vi.mock('panel/common/ui/Link', () => ({
    Link: (props: any) => <a class={props.class} href={props.to} />,
}));

vi.mock('panel/api/generated', () => ({
    getLogoutUrl: (): string => 'control/logout',
}));

vi.mock('./AccordionSection', () => ({
    AccordionSection: (): null => null,
}));

import { Menu } from 'panel/common/ui/Menu';

const renderMenu = () =>
    render(() => (
        <Menu
            accountSubMenu={false}
            setAccountSubMenu={vi.fn()}
            rightSideDropdown={false}
            closeSubMenu={vi.fn()}
        />
    ));

describe('Menu logout', () => {
    beforeEach(() => {
        vi.clearAllMocks();
    });

    it('renders a logout button', () => {
        renderMenu();

        const label = screen.getByText('Log out');
        expect(label.closest('button')).not.toBeNull();
        expect(label.closest('a')).toBeNull();
    });

    it('replaces the current location with /control/logout on click', async () => {
        const user = userEvent.setup();
        const replace = vi.fn();
        Object.defineProperty(window, 'location', {
            configurable: true,
            value: { replace },
        });

        renderMenu();

        await user.click(screen.getByText('Log out'));

        expect(replace).toHaveBeenCalledWith('/control/logout');
    });
});