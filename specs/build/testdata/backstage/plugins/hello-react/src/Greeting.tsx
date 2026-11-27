import { greeting } from '@hello/common';

/**
 * Shows the greeting for a name.
 *
 * @public
 */
export function Greeting(props: { name: string }) {
  return <p>{greeting(props.name)}</p>;
}
