import type { Meta, StoryObj } from '@storybook/react-vite';
import { Button } from 'antd';
import { DeleteOutlined, EditOutlined, KeyOutlined, UserAddOutlined } from '@ant-design/icons';
import { expect } from 'storybook/test';

import PlanCard from './PlanCard';

const actions = [
  <Button key="codes" type="text" icon={<KeyOutlined />}>
    Activation codes
  </Button>,
  <Button key="assign" type="text" icon={<UserAddOutlined />}>
    Add users
  </Button>,
  <Button key="edit" type="text" icon={<EditOutlined />}>
    Edit
  </Button>,
  <Button key="delete" type="text" danger icon={<DeleteOutlined />}>
    Delete
  </Button>,
];

const standard = {
  name: 'Standard',
  template: 'relay_v3',
  ipLimit: 'Unlimited',
  terms: '月付 · 季付 · 半年付 · 年付',
  nodes: '3',
  members: '12 user(s)',
  actions,
};

const meta = {
  title: 'Plans/PlanCard',
  component: PlanCard,
  tags: ['autodocs'],
  decorators: [
    (Story, { parameters }) => (
      <div
        className="plan-grid"
        style={{
          width: parameters.gridWidth ?? 380,
          gridTemplateColumns: `repeat(${parameters.columns ?? 1}, minmax(0, 1fr))`,
        }}
      >
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'padded',
    docs: {
      description: {
        component:
          'One plan on the Plans page: its name on one line with the remark as a tooltip, five facts on fixed-height rows, and its four actions in an even two-by-two bar on the bottom edge, every action centred in a cell of the same size. The page renders the facts and the actions and passes them in. Lay cards out with the `plan-grid` class that ships with it: every card in a row then has the same size.',
      },
    },
  },
  argTypes: {
    name: { description: 'The plan name, cut to one line.' },
    remark: { description: 'Optional note, shown as a tooltip on an icon beside the name.' },
    template: { description: 'The rule template row.' },
    ipLimit: { description: 'The IP limit row.' },
    terms: { description: 'The billing terms row.' },
    nodes: { description: 'The nodes row, e.g. a count with the node names as a tooltip.' },
    members: { description: "The users row, e.g. a link to the plan's users." },
    actions: {
      control: false,
      description:
        'Four actions, two by two in reading order. A label too long for its cell wraps.',
    },
  },
  args: standard,
} satisfies Meta<typeof PlanCard>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Default: Story = {};

// Each action's column and row in a card's bar, counted from the bar's left edge.
function placesOf(bar: HTMLElement): string[] {
  const box = bar.getBoundingClientRect();
  const cells = Array.from(bar.children, (cell) => cell.getBoundingClientRect());
  const tops = [...new Set(cells.map((cell) => Math.round(cell.top)))].sort((a, b) => a - b);
  return cells.map((cell) => {
    const column = cell.left - box.left < box.width / 4 ? 1 : 2;
    return `${column}/${tops.indexOf(Math.round(cell.top)) + 1}`;
  });
}

// The dividers each action's cell draws: on its end edge, its top edge, or none.
function dividersOf(bar: HTMLElement): string[] {
  return Array.from(bar.children, (cell) => {
    const style = getComputedStyle(cell);
    const sides: string[] = [];
    if (parseFloat(style.borderInlineEndWidth) > 0) sides.push('end');
    if (parseFloat(style.borderTopWidth) > 0) sides.push('top');
    return sides.join('+') || 'none';
  });
}

// How far each action's button sits from the middle of its cell, in whole pixels.
function offCentreOf(bar: HTMLElement): number[] {
  return Array.from(bar.children, (cell) => {
    const box = cell.getBoundingClientRect();
    const found = cell.querySelector('.ant-btn');
    if (!found) throw new Error('an action cell holds no button');
    const button = found.getBoundingClientRect();
    return Math.round(
      Math.max(
        Math.abs(button.left + button.width / 2 - (box.left + box.width / 2)),
        Math.abs(button.top + button.height / 2 - (box.top + box.height / 2)),
      ),
    );
  });
}

// Gaps between the cells and at the bar's sides and bottom, so the dividers meet.
function gapsOf(bar: HTMLElement): number[] {
  const box = bar.getBoundingClientRect();
  const [first, second, third] = Array.from(bar.children, (cell) => cell.getBoundingClientRect());
  return [
    first.left - box.left,
    second.left - first.right,
    box.right - second.right,
    third.top - first.bottom,
    box.bottom - third.bottom,
  ].map((gap) => Math.round(Math.abs(gap)));
}

function spread(values: number[]): number {
  return Math.max(...values) - Math.min(...values);
}

// Narrow cards, where "Activation codes" wraps onto two lines, with facts of every length.
export const EvenGrid: Story = {
  parameters: { gridWidth: 560, columns: 2 },
  render: () => (
    <>
      <PlanCard {...standard} />
      <PlanCard
        {...standard}
        name="A plan with a name far too long for one line of a card"
        remark="Shared residential line, sold quarterly or yearly"
        template="None"
        ipLimit="2"
        terms="季付 · 年付"
        nodes="0"
        members="0 user(s)"
      />
      <PlanCard
        {...standard}
        name="Trial"
        template="alpha_v3"
        ipLimit="1"
        terms="不限"
        nodes="1"
        members="1 user(s)"
      />
    </>
  ),
  play: async ({ canvasElement }) => {
    const bars = Array.from(
      canvasElement.querySelectorAll<HTMLElement>('.plan-card .ant-card-actions'),
    );
    await expect(bars).toHaveLength(3);
    for (const bar of bars) {
      const cells = Array.from(bar.children, (cell) => cell.getBoundingClientRect());
      await expect(placesOf(bar)).toEqual(['1/1', '2/1', '1/2', '2/2']);
      await expect(dividersOf(bar)).toEqual(['end', 'none', 'end+top', 'top']);
      await expect(gapsOf(bar)).toEqual([0, 0, 0, 0, 0]);
      await expect(spread(cells.map((cell) => cell.width))).toBeLessThanOrEqual(1);
      await expect(spread(cells.map((cell) => cell.height))).toBeLessThanOrEqual(1);
      await expect(Math.max(...offCentreOf(bar))).toBeLessThanOrEqual(1);
    }
  },
};
